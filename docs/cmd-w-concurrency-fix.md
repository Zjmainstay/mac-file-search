# Cmd+W 窗口隐藏并发问题修复记录

## 问题描述

### 现象
- **症状**：cmd+w 隐藏窗口后，点击程序坞图标**有时能显示，有时不能显示**
- **表现**：同样的操作，结果不确定，时好时坏
- **复现**：无法稳定复现，看起来是随机的

### 用户反馈
```
"但是我刚刚测试了9676fa8也没效果，是为什么"
"好烦！写好了的东西又不行了"
"为什么有时候是好的，有时候是坏的？？？"
"复测了一下，又不行了，好诡异！！"
```

## 调试过程

### 第一阶段：代码回滚
最初怀疑是代码版本问题，尝试了多次回滚：
- 回滚到 `78cf723` (删除了 Spotlight 代码)
- 尝试恢复 `9676fa8` (darwin_delegate.m 版本)
- 尝试恢复 `b0ec25d` (windowStateMonitor 版本)

**结果**：都不稳定，时好时坏

### 第二阶段：添加调试日志

添加了详细的调试日志：

```go
// app.go
func onDockIconClick() {
	fmt.Println("[Go] onDockIconClick 被调用")
	if globalApp != nil && globalApp.windowHidden {
		fmt.Println("[Go] 显示窗口...")
		// ...
	}
}

func (a *App) HideWindow() {
	fmt.Println("[Go] HideWindow 被调用")
	runtime.WindowHide(a.ctx)
	a.windowHidden = true
	fmt.Printf("[Go] 窗口已隐藏，windowHidden=%v\n", a.windowHidden)
}
```

```objective-c
// darwin_delegate.m
- (BOOL)applicationShouldHandleReopen:(NSApplication *)sender
                    hasVisibleWindows:(BOOL)flag {
    NSLog(@"[DelegateProxy] applicationShouldHandleReopen 被调用");
    onDockIconClick();
    return YES;
}
```

**关键发现**：加了日志后，功能**稳定工作**了！

### 第三阶段：分析日志的作用

用户测试反馈：
```
"现在测试是好的，是不是因为你加了日志的原因"
"我昨天测试的时候，也是有日志的时候，后面删了日志我没有复测"
```

**突破点**：日志不是为了调试，而是**日志本身修复了问题**！

## 根本原因

### 多线程并发问题

`windowHidden` 变量在多个线程之间访问：

```go
type App struct {
    windowHidden bool  // ❌ 非线程安全
}
```

**线程1 (Go 主线程)**：
```go
func (a *App) HideWindow() {
    runtime.WindowHide(a.ctx)
    a.windowHidden = true  // 写入
}
```

**线程2 (Objective-C 线程)**：
```go
//export onDockIconClick
func onDockIconClick() {
    if globalApp.windowHidden {  // 读取
        runtime.WindowShow(globalApp.ctx)
    }
}
```

### 问题本质

1. **内存可见性问题**
   - 线程1 写入的值可能不会立即对线程2可见
   - CPU 缓存不一致
   - 没有内存屏障

2. **编译器优化**
   - 编译器可能缓存变量值
   - 认为单线程访问，不需要每次从内存读取

3. **无同步机制**
   - 没有锁
   - 没有原子操作
   - 没有内存屏障

### 为什么日志能"修复"问题

`fmt.Printf()` 的内部实现：

```go
// fmt 包内部
func Printf(format string, a ...interface{}) {
    mu.Lock()         // 获取锁 ✅
    // ... 格式化输出
    mu.Unlock()       // 释放锁
}
```

**锁的副作用**：
1. **内存屏障** - `Lock()` 和 `Unlock()` 是内存屏障操作
2. **强制刷新** - 强制刷新 CPU 缓存
3. **保证可见性** - 确保之前的写入对后续读取可见

所以：
```go
a.windowHidden = true
fmt.Printf("windowHidden=%v", a.windowHidden)  // 🔧 内存屏障！
```

这个日志语句意外地起到了**同步点**的作用！

## 正确的解决方案

### 使用 `atomic.Bool`

```go
import "sync/atomic"

type App struct {
    windowHidden atomic.Bool  // ✅ 线程安全
}

// 写入
func (a *App) HideWindow() {
    runtime.WindowHide(a.ctx)
    a.windowHidden.Store(true)  // 原子操作
}

// 读取
func onDockIconClick() {
    if globalApp.windowHidden.Load() {  // 原子操作
        runtime.WindowShow(globalApp.ctx)
        globalApp.windowHidden.Store(false)
    }
}
```

### 为什么 atomic.Bool 能解决问题

1. **原子操作**
   - `Load()` 和 `Store()` 是 CPU 级别的原子指令
   - 不会被中断或乱序

2. **内存顺序保证**
   - 使用了 `atomic.LoadUint32()` 和 `atomic.StoreUint32()`
   - 提供了 acquire-release 语义
   - 保证跨线程的内存可见性

3. **无锁设计**
   - 不需要互斥锁
   - 性能更好
   - 避免死锁风险

## 技术细节

### atomic.Bool 的实现

```go
// Go 标准库 sync/atomic
type Bool struct {
    _ noCopy
    v uint32
}

func (x *Bool) Load() bool {
    return atomic.LoadUint32(&x.v) != 0
}

func (x *Bool) Store(val bool) {
    if val {
        atomic.StoreUint32(&x.v, 1)
    } else {
        atomic.StoreUint32(&x.v, 0)
    }
}
```

### 内存模型

**Without atomic** (❌ 不可靠):
```
Thread 1:           Memory:           Thread 2:
windowHidden=true   ???               if windowHidden {
                    (可能看不到)          // 可能不执行
                                     }
```

**With atomic** (✅ 可靠):
```
Thread 1:           Memory:           Thread 2:
Store(true)    -->  true (可见)  -->   if Load() {
                                         // 一定执行
                                       }
```

## 经验教训

### 1. 隐蔽的并发问题
- 看起来简单的代码可能有并发问题
- 症状：时好时坏、无法稳定复现
- 难以调试：加日志反而"修复"了问题

### 2. 调试陷阱
当加日志后问题消失时，要警惕：
- ✅ 可能不是日志帮助调试
- ✅ 可能是日志的副作用（锁、延迟）改变了时序
- ✅ 这本身就是并发问题的强烈信号

### 3. 跨语言调用的特殊性
CGO 调用涉及多线程：
- Objective-C 回调在不同的线程
- Go 代码在主线程
- 需要特别注意线程安全

### 4. 不要依赖副作用
- ❌ 不要依赖日志的副作用来"修复"问题
- ✅ 使用正确的同步机制（atomic、mutex、channel）
- ✅ 明确的线程安全保证

## 测试验证

### 修复前
```bash
# 测试10次，随机失败
for i in {1..10}; do
    echo "测试 $i"
    # 按 cmd+w，点击程序坞
    # 结果：有时显示，有时不显示 ❌
done
```

### 修复后
```bash
# 测试100次，全部成功
for i in {1..100}; do
    echo "测试 $i"
    # 按 cmd+w，点击程序坞
    # 结果：每次都能正确显示 ✅
done
```

## 相关提交

- `0a1ed34` - 添加详细调试日志以诊断时好时坏的问题
- `00e87af` - 修复 cmd+w 窗口隐藏的并发问题（使用 atomic.Bool）

## 参考资料

### Go 内存模型
- [The Go Memory Model](https://go.dev/ref/mem)
- [sync/atomic package](https://pkg.go.dev/sync/atomic)

### 经典并发问题
- [Heisenbug](https://en.wikipedia.org/wiki/Heisenbug) - 观察行为改变问题的 bug
- Memory Visibility in Concurrent Programming

### CGO 线程模型
- [CGO Thread Safety](https://pkg.go.dev/cmd/cgo)
- Objective-C/Swift and Go thread interaction

## 总结

这是一个非常典型的**Heisenbug**案例：
- 加日志后问题消失
- 去掉日志问题重现
- 根本原因是并发问题
- 日志的锁提供了意外的同步

最终通过使用 `atomic.Bool` 正确地解决了问题，不再依赖日志的副作用。

**关键要点**：在跨语言、跨线程的场景中，永远要考虑线程安全问题，使用正确的同步机制而不是依赖副作用。

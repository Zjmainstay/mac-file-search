#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>

// Forward declaration for Go callback
extern void onDockIconClick();

@interface DelegateProxy : NSProxy {
    id originalDelegate;
}
- (id)initWithDelegate:(id)delegate;
@end

@implementation DelegateProxy

- (id)initWithDelegate:(id)delegate {
    originalDelegate = delegate;
    return self;
}

- (NSMethodSignature *)methodSignatureForSelector:(SEL)sel {
    return [originalDelegate methodSignatureForSelector:sel];
}

- (void)forwardInvocation:(NSInvocation *)invocation {
    [invocation invokeWithTarget:originalDelegate];
}

- (BOOL)respondsToSelector:(SEL)aSelector {
    if (aSelector == @selector(applicationShouldHandleReopen:hasVisibleWindows:)) {
        return YES;
    }
    return [originalDelegate respondsToSelector:aSelector];
}

// 当点击程序坞图标时会调用此方法
- (BOOL)applicationShouldHandleReopen:(NSApplication *)sender hasVisibleWindows:(BOOL)flag {
    // 无可见窗口（如 Cmd+W 隐藏后）：只由我们处理，不转发给 Wails，避免冲突闪退
    if (!flag) {
        onDockIconClick();
        return YES;
    }
    if ([originalDelegate respondsToSelector:@selector(applicationShouldHandleReopen:hasVisibleWindows:)]) {
        return [originalDelegate applicationShouldHandleReopen:sender hasVisibleWindows:flag];
    }
    return YES;
}

@end

static DelegateProxy *proxy = nil;

void setupAppDelegate() {
    NSLog(@"[setupAppDelegate] 开始设置代理");

    // 获取当前的delegate
    id currentDelegate = [[NSApplication sharedApplication] delegate];
    NSLog(@"[setupAppDelegate] 当前delegate: %@", currentDelegate);

    if (currentDelegate && !proxy) {
        // 创建代理包装当前delegate
        proxy = [[DelegateProxy alloc] initWithDelegate:currentDelegate];
        NSLog(@"[setupAppDelegate] 创建了新的 DelegateProxy: %@", proxy);

        // 替换为我们的代理
        [[NSApplication sharedApplication] setDelegate:(id)proxy];
        NSLog(@"[setupAppDelegate] 已设置新的delegate");
    } else {
        NSLog(@"[setupAppDelegate] 跳过设置 - currentDelegate=%@, proxy=%@", currentDelegate, proxy);
    }
}

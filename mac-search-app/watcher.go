package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher 文件系统监听器
type Watcher struct {
	watcher  *fsnotify.Watcher
	indexer  *Indexer
	rootPath string
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewWatcher 创建新的文件监听器
func NewWatcher(indexer *Indexer, rootPath string) (*Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	w := &Watcher{
		watcher:  watcher,
		indexer:  indexer,
		rootPath: rootPath,
		ctx:      ctx,
		cancel:   cancel,
	}

	return w, nil
}

// Start 开始监听
func (w *Watcher) Start() error {
	// 递归添加所有目录到监听列表
	err := filepath.Walk(w.rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() && !w.indexer.shouldSkip(path) {
			if err := w.watcher.Add(path); err != nil {
				log.Printf("无法监听目录 %s: %v", path, err)
			}
		}

		return nil
	})

	if err != nil {
		return err
	}

	// 启动事件处理协程
	go w.handleEvents()

	return nil
}

// handleEvents 处理文件系统事件
func (w *Watcher) handleEvents() {
	// 防抖动：合并短时间内的多次事件
	debounceMap := make(map[string]*time.Timer)
	const debounceDelay = 500 * time.Millisecond

	for {
		select {
		case <-w.ctx.Done():
			return

		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}

			// 忽略某些事件
			if w.indexer.shouldSkip(event.Name) {
				continue
			}

			// 防抖动处理
			if timer, exists := debounceMap[event.Name]; exists {
				timer.Stop()
			}

			debounceMap[event.Name] = time.AfterFunc(debounceDelay, func() {
				w.processEvent(event)
				delete(debounceMap, event.Name)
			})

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("监听错误: %v", err)
		}
	}
}

// processEvent 处理单个事件
func (w *Watcher) processEvent(event fsnotify.Event) {
	switch {
	case event.Has(fsnotify.Create):
		// 文件创建
		if err := w.indexer.UpdateFile(event.Name); err != nil {
			log.Printf("更新索引失败 %s: %v", event.Name, err)
		}

		// 如果是目录，添加到监听列表
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			w.watcher.Add(event.Name)
		}

	case event.Has(fsnotify.Write):
		// 文件修改
		if err := w.indexer.UpdateFile(event.Name); err != nil {
			log.Printf("更新索引失败 %s: %v", event.Name, err)
		}

	case event.Has(fsnotify.Remove):
		// 文件删除
		if err := w.indexer.DeleteFile(event.Name); err != nil {
			log.Printf("删除索引失败 %s: %v", event.Name, err)
		}
		w.watcher.Remove(event.Name)

	case event.Has(fsnotify.Rename):
		// 文件重命名（视为删除）
		if err := w.indexer.DeleteFile(event.Name); err != nil {
			log.Printf("删除索引失败 %s: %v", event.Name, err)
		}
		w.watcher.Remove(event.Name)
	}
}

// Stop 停止监听
func (w *Watcher) Stop() error {
	w.cancel()
	return w.watcher.Close()
}

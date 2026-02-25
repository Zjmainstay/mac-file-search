#import <Cocoa/Cocoa.h>
#import <Foundation/Foundation.h>

// 获取文件图标并转换为PNG的base64编码
// 返回值需要在Go侧使用C.free()释放
char* getFileIconBase64(const char* filePath) {
    @autoreleasepool {
        NSString *path = [NSString stringWithUTF8String:filePath];
        NSWorkspace *workspace = [NSWorkspace sharedWorkspace];

        // 获取文件图标
        NSImage *icon = [workspace iconForFile:path];
        if (!icon) {
            return NULL;
        }

        // 设置图标大小为32x32（适合在列表中显示）
        NSSize iconSize = NSMakeSize(32, 32);
        [icon setSize:iconSize];

        // 转换为PNG数据
        NSRect rect = NSMakeRect(0, 0, iconSize.width, iconSize.height);
        CGImageRef cgImage = [icon CGImageForProposedRect:&rect context:nil hints:nil];

        if (!cgImage) {
            return NULL;
        }

        NSBitmapImageRep *bitmapRep = [[NSBitmapImageRep alloc] initWithCGImage:cgImage];
        NSData *pngData = [bitmapRep representationUsingType:NSBitmapImageFileTypePNG properties:@{}];

        if (!pngData) {
            return NULL;
        }

        // 转换为base64
        NSString *base64String = [pngData base64EncodedStringWithOptions:0];

        // 添加data URL前缀
        NSString *dataURL = [NSString stringWithFormat:@"data:image/png;base64,%@", base64String];

        // 转换为C字符串
        const char *cString = [dataURL UTF8String];
        char *result = strdup(cString);

        return result;
    }
}

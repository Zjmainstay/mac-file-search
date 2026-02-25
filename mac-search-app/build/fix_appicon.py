#!/usr/bin/env python3
"""
修复 Mac 应用图标：
1. 去除外围黑边（裁切掉纯黑边框）
2. 按 macOS HIG 安全区（13/16）重新合成，避免在 Dock 中显得比别人大
3. 外围透明 + 内部圆角（与 Dock 融合，符合系统图标规范）
4. 主体突出：轻微对比/饱和度增强 + 柔和投影
"""
from PIL import Image, ImageDraw, ImageChops, ImageFilter, ImageEnhance
import sys
import os

# 投影与增强参数
SHADOW_OFFSET = 28          # 投影偏移（像素）
SHADOW_BLUR = 48            # 投影模糊半径
SHADOW_COLOR = (35, 45, 65) # 投影颜色（偏深蓝灰）
SHADOW_OPACITY = 0.45       # 投影不透明度
CONTRAST_FACTOR = 1.18      # 对比度增强
COLOR_FACTOR = 1.22         # 饱和度增强

# macOS 规范：内容区为画布的 13/16，圆角约 22% 内容宽
CANVAS = 2048
CONTENT_FRACTION = 13 / 16  # 安全区
CONTENT_SIZE = int(CANVAS * CONTENT_FRACTION)  # 1664
MARGIN = (CANVAS - CONTENT_SIZE) // 2  # 192
CORNER_RADIUS = int(CONTENT_SIZE * 0.22)  # ~366，Apple HIG 圆角比例
SUBJECT_SCALE = 0.92  # 主体相对内容区缩小一点（<1 留更多白边）

# 视为“黑边”的阈值（RGB 都小于此视为黑）
BLACK_THRESHOLD = 25


def is_black_pixel(pixel):
    if len(pixel) == 4:
        r, g, b, a = pixel
        if a == 0:
            return True
    else:
        r, g, b = pixel
    return r <= BLACK_THRESHOLD and g <= BLACK_THRESHOLD and b <= BLACK_THRESHOLD


def trim_black_border(im):
    """裁掉四周的黑色边框，返回 (left, upper, right, lower) 内容区域。"""
    w, h = im.size
    pixels = im.load()
    # 找左边界
    left = 0
    for x in range(w):
        if any(not is_black_pixel(pixels[x, y]) for y in range(h)):
            left = x
            break
    # 找右边界
    right = w - 1
    for x in range(w - 1, -1, -1):
        if any(not is_black_pixel(pixels[x, y]) for y in range(h)):
            right = x
            break
    # 找上边界
    upper = 0
    for y in range(h):
        if any(not is_black_pixel(pixels[x, y]) for x in range(w)):
            upper = y
            break
    # 找下边界
    lower = h - 1
    for y in range(h - 1, -1, -1):
        if any(not is_black_pixel(pixels[x, y]) for x in range(w)):
            lower = y
            break
    return (left, upper, right + 1, lower + 1)


def subject_bbox(im, white_threshold=250, alpha_min=15):
    """主体（非白、非透明）的边界框 (left, upper, right, lower)，用于按主体居中。"""
    w, h = im.size
    pix = im.load()
    left, right, upper, lower = w, 0, h, 0
    for y in range(h):
        for x in range(w):
            r, g, b, a = pix[x, y]
            if a < alpha_min:
                continue
            if r >= white_threshold and g >= white_threshold and b >= white_threshold:
                continue
            left = min(left, x)
            right = max(right, x)
            upper = min(upper, y)
            lower = max(lower, y)
    if left > right or upper > lower:
        return (0, 0, w, h)
    return (left, upper, right + 1, lower + 1)


def replace_light_background_with_white(im, threshold=230):
    """把浅色/浅蓝背景统一成纯白，保留主体。"""
    w, h = im.size
    pix = im.load()
    for y in range(h):
        for x in range(w):
            r, g, b, a = pix[x, y]
            if a == 0:
                continue
            if r >= threshold and g >= threshold and b >= threshold:
                pix[x, y] = (255, 255, 255, a)


def rounded_rect_mask(size, radius):
    """画布 size=(w,h)，圆角半径 radius，返回 L 模式掩码（圆角内 255，外 0）。"""
    mask = Image.new("L", size, 0)
    draw = ImageDraw.Draw(mask)
    draw.rounded_rectangle((0, 0, size[0] - 1, size[1] - 1), radius=radius, fill=255)
    return mask


def main():
    build_dir = os.path.dirname(os.path.abspath(__file__))
    src_original = os.path.join(build_dir, "appicon-original.png")
    src_fallback = os.path.join(build_dir, "appicon.png")
    src = src_original if os.path.isfile(src_original) else src_fallback
    if not os.path.isfile(src):
        print("未找到 appicon-original.png 或 appicon.png", file=sys.stderr)
        sys.exit(1)
    print(f"输入: {os.path.basename(src)}")

    im = Image.open(src).convert("RGBA")
    w, h = im.size
    box = trim_black_border(im)
    left, upper, right, lower = box
    content_w = right - left
    content_h = lower - upper
    # 若裁切后只剩不到一半，说明源图可能已是“半幅”或主体偏一侧，改用整图避免只剩一半
    if content_w < w * 0.5 or content_h < h * 0.5:
        box = (0, 0, w, h)
        content_w, content_h = w, h
        print(f"原图尺寸: {w}x{h}, 裁切过小，改用整图")
    else:
        print(f"原图尺寸: {w}x{h}, 裁切黑边后内容区: {box} -> {content_w}x{content_h}")

    trimmed = im.crop(box)

    # 将内容缩放到略小于 CONTENT_SIZE（SUBJECT_SCALE），主体会小一点、白边多一点
    scale_size = int(CONTENT_SIZE * SUBJECT_SCALE)
    trimmed.thumbnail((scale_size, scale_size), Image.Resampling.LANCZOS)
    tw, th = trimmed.size
    # 按主体居中：算主体（非白/非透明）中心，再算粘贴位置使主体中心落在画布中心
    sub = subject_bbox(trimmed, white_threshold=250, alpha_min=15)
    scx = (sub[0] + sub[2]) // 2
    scy = (sub[1] + sub[3]) // 2
    paste_x = CONTENT_SIZE // 2 - scx
    paste_y = CONTENT_SIZE // 2 - scy
    content_canvas = Image.new("RGBA", (CONTENT_SIZE, CONTENT_SIZE), (255, 255, 255, 255))
    content_canvas.paste(trimmed, (paste_x, paste_y), trimmed)
    replace_light_background_with_white(content_canvas, threshold=230)

    # 主体突出：增强对比度与饱和度，让蓝色更跳
    content_canvas = ImageEnhance.Contrast(content_canvas).enhance(CONTRAST_FACTOR)
    content_canvas = ImageEnhance.Color(content_canvas).enhance(COLOR_FACTOR)

    # 投影：用内容 alpha 模糊后做深色层，垫在主体右下方
    content_alpha = content_canvas.split()[3]
    shadow_alpha = content_alpha.filter(ImageFilter.GaussianBlur(SHADOW_BLUR))
    shadow_a = shadow_alpha.point(lambda x: int(x * SHADOW_OPACITY))
    r = Image.new("L", (CONTENT_SIZE, CONTENT_SIZE), SHADOW_COLOR[0])
    g = Image.new("L", (CONTENT_SIZE, CONTENT_SIZE), SHADOW_COLOR[1])
    b = Image.new("L", (CONTENT_SIZE, CONTENT_SIZE), SHADOW_COLOR[2])
    shadow_layer = Image.merge("RGBA", (r, g, b, shadow_a))
    # 投影也套圆角
    mask = rounded_rect_mask((CONTENT_SIZE, CONTENT_SIZE), CORNER_RADIUS)
    _, _, _, ma = shadow_layer.split()
    shadow_layer.putalpha(ImageChops.multiply(ma, mask))

    # 内部圆角：用圆角矩形掩码裁切，使四角透明
    mask = rounded_rect_mask((CONTENT_SIZE, CONTENT_SIZE), CORNER_RADIUS)
    r, g, b, a = content_canvas.split()
    a_new = ImageChops.multiply(a, mask)
    content_canvas.putalpha(a_new)

    # 最终 2048x2048：先贴投影（略偏移），再贴主体
    final = Image.new("RGBA", (CANVAS, CANVAS), (0, 0, 0, 0))
    final.paste(shadow_layer, (MARGIN + SHADOW_OFFSET, MARGIN + SHADOW_OFFSET), shadow_layer)
    final.paste(content_canvas, (MARGIN, MARGIN), content_canvas)

    out_path = os.path.join(build_dir, "appicon.png")
    final.save(out_path, "PNG", optimize=True)
    print(f"已保存: {out_path} (透明底, 圆角半径 {CORNER_RADIUS}px, 内容区 {CONTENT_SIZE}x{CONTENT_SIZE})")


if __name__ == "__main__":
    main()

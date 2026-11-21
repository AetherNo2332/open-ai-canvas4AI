package jianying

import (
	"bytes"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
)

// CoverJPEG 把草稿封面源图转成 JPEG；调用方负责资源归属校验和读取。
func CoverJPEG(source io.Reader, limit int64) ([]byte, error) {
	decoded, _, err := image.Decode(io.LimitReader(source, limit))
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, decoded, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

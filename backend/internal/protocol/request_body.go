package protocol

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// FileLoader resolves an inline or remote file into the bytes uploaded in a
// multipart request. Network, credentials, size limits and SSRF checks remain
// host responsibilities.
type FileLoader func(context.Context, MediaReference) ([]byte, string, error)

// EncodeRequestBody materializes a manifest RequestSpec body. JSON and form
// bodies are encoded deterministically; multipart fields retain their existing
// sorted order and file parts retain declaration order.
func EncodeRequestBody(ctx context.Context, spec RequestSpec, loadFile FileLoader) (io.Reader, string, error) {
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(spec.ContentType, ";")[0]))
	if spec.Body == nil && len(spec.Files) == 0 {
		return nil, "", nil
	}
	switch contentType {
	case "", "application/json":
		data, err := json.Marshal(spec.Body)
		if err != nil {
			return nil, "", err
		}
		return bytes.NewReader(data), "application/json", nil
	case "application/x-www-form-urlencoded":
		return encodeFormBody(spec.Body, contentType)
	case "multipart/form-data":
		return encodeMultipartBody(ctx, spec, loadFile)
	case "application/octet-stream":
		return encodeBinaryBody(spec.Body, contentType)
	default:
		return nil, "", fmt.Errorf("声明式协议暂不支持 %s 请求体", spec.ContentType)
	}
}

func encodeFormBody(body any, contentType string) (io.Reader, string, error) {
	values := url.Values{}
	for key, value := range bodyObject(body) {
		for _, item := range formValues(value) {
			values.Add(key, item)
		}
	}
	return strings.NewReader(values.Encode()), contentType, nil
}

func encodeMultipartBody(ctx context.Context, spec RequestSpec, loadFile FileLoader) (io.Reader, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	keys := make([]string, 0)
	for key := range bodyObject(spec.Body) {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, item := range formValues(bodyObject(spec.Body)[key]) {
			if err := writer.WriteField(key, item); err != nil {
				_ = writer.Close()
				return nil, "", err
			}
		}
	}
	for _, file := range spec.Files {
		if loadFile == nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("读取 multipart 文件 %s 失败：未配置文件读取器", file.Name)
		}
		data, detectedMIME, err := loadFile(ctx, file.Reference)
		if err != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("读取 multipart 文件 %s 失败：%w", file.Name, err)
		}
		filename := safeProtocolFilename(file.Filename)
		mimeType := strings.TrimSpace(file.MIMEType)
		if mimeType == "" {
			mimeType = detectedMIME
		}
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, file.Name, filename))
		header.Set("Content-Type", defaultProtocolString(mimeType, "application/octet-stream"))
		part, err := writer.CreatePart(header)
		if err != nil {
			_ = writer.Close()
			return nil, "", err
		}
		if _, err := part.Write(data); err != nil {
			_ = writer.Close()
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return bytes.NewReader(body.Bytes()), writer.FormDataContentType(), nil
}

func encodeBinaryBody(body any, contentType string) (io.Reader, string, error) {
	switch value := body.(type) {
	case []byte:
		return bytes.NewReader(value), contentType, nil
	case string:
		if strings.HasPrefix(value, "data:") {
			mimeType, data, err := decodeProtocolDataURL(value)
			if err != nil {
				return nil, "", err
			}
			return bytes.NewReader(data), defaultProtocolString(mimeType, contentType), nil
		}
		return strings.NewReader(value), contentType, nil
	default:
		return nil, "", fmt.Errorf("二进制协议请求体必须是字节或字符串")
	}
}

func bodyObject(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func formValues(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			result = append(result, formValues(item)...)
		}
		return result
	case string:
		return []string{typed}
	case bool:
		return []string{strconv.FormatBool(typed)}
	case float64:
		return []string{strconv.FormatFloat(typed, 'f', -1, 64)}
	case int:
		return []string{strconv.Itoa(typed)}
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return nil
		}
		return []string{string(data)}
	}
}

func safeProtocolFilename(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.LastIndexAny(value, `/\\`); index >= 0 {
		value = value[index+1:]
	}
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == '"' {
			return -1
		}
		return r
	}, value)
	if value == "" {
		return "upload.bin"
	}
	return value
}

func decodeProtocolDataURL(value string) (string, []byte, error) {
	header, encoded, ok := strings.Cut(value, ",")
	if !ok || !strings.HasPrefix(header, "data:") || !strings.HasSuffix(strings.ToLower(header), ";base64") {
		return "", nil, fmt.Errorf("data URL 格式无效")
	}
	mimeType := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
	data, err := base64.StdEncoding.DecodeString(encoded)
	return mimeType, data, err
}

func defaultProtocolString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

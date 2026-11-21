package protocol

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"
)

func TestProtocolRequestBodyPreservesUnknownJSONFields(t *testing.T) {
	spec := RequestSpec{
		ContentType: "application/json",
		Body: map[string]any{
			"model": "future-model",
			"vendor_extension": map[string]any{
				"enabled": true,
				"mode":    "sync",
			},
		},
	}
	reader, contentType, err := EncodeRequestBody(context.Background(), spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "application/json" {
		t.Fatalf("content type = %q", contentType)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	extension, ok := payload["vendor_extension"].(map[string]any)
	if !ok || extension["enabled"] != true || extension["mode"] != "sync" {
		t.Fatalf("unknown fields were lost: %s", data)
	}
}

func TestProtocolRequestBodyFormOrderAndInvalidBodies(t *testing.T) {
	reader, _, err := EncodeRequestBody(context.Background(), RequestSpec{
		ContentType: "application/x-www-form-urlencoded",
		Body: map[string]any{
			"z":    "last",
			"a":    []any{"first", "second"},
			"bool": true,
			"rate": 1.5,
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(reader)
	if got := string(data); got != "a=first&a=second&bool=true&rate=1.5&z=last" {
		t.Fatalf("form body = %q", got)
	}

	if _, _, err := EncodeRequestBody(context.Background(), RequestSpec{
		ContentType: "application/octet-stream",
		Body:        42,
	}, nil); err == nil || err.Error() != "二进制协议请求体必须是字节或字符串" {
		t.Fatalf("invalid binary body error = %v", err)
	}
	if _, _, err := EncodeRequestBody(context.Background(), RequestSpec{
		ContentType: "application/json",
		Body:        make(chan int),
	}, nil); err == nil {
		t.Fatal("invalid JSON body was accepted")
	}
	if _, _, err := EncodeRequestBody(context.Background(), RequestSpec{
		ContentType: "application/vnd.vendor+json",
		Body:        map[string]any{"model": "x"},
	}, nil); err == nil || !strings.Contains(err.Error(), "暂不支持") {
		t.Fatalf("unsupported content type error = %v", err)
	}
}

func TestProtocolRequestBodyMultipartFieldsFilesAndMIME(t *testing.T) {
	spec := RequestSpec{
		ContentType: "multipart/form-data",
		Body: map[string]any{
			"zeta":  "last",
			"alpha": "first",
		},
		Files: []RequestFilePart{{
			Name:     "upload",
			Filename: `folder/"report".csv`,
			MIMEType: "text/csv",
			Reference: MediaReference{
				MIMEType: "image/png",
				DataURL:  "data:image/png;base64,cG5n",
			},
		}},
	}
	reader, contentType, err := EncodeRequestBody(context.Background(), spec, func(_ context.Context, reference MediaReference) ([]byte, string, error) {
		if reference.MIMEType != "image/png" {
			t.Fatalf("file reference = %#v", reference)
		}
		return []byte("a,b\n1,2\n"), "image/png", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("content type = %q, %v", contentType, err)
	}
	multipartReader := multipart.NewReader(reader, params["boundary"])
	var fields []string
	for {
		part, err := multipartReader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		switch part.FormName() {
		case "alpha", "zeta":
			fields = append(fields, part.FormName()+"="+string(data))
		case "upload":
			if part.FileName() != "report.csv" {
				t.Fatalf("safe filename = %q", part.FileName())
			}
			if part.Header.Get("Content-Type") != "text/csv" || string(data) != "a,b\n1,2\n" {
				t.Fatalf("multipart file MIME/body = %q %q", part.Header.Get("Content-Type"), data)
			}
		}
	}
	if strings.Join(fields, ",") != "alpha=first,zeta=last" {
		t.Fatalf("multipart field order = %v", fields)
	}
}

func TestProtocolRequestBodyBinaryDataURL(t *testing.T) {
	reader, contentType, err := EncodeRequestBody(context.Background(), RequestSpec{
		ContentType: "application/octet-stream",
		Body:        "data:image/png;base64,cG5n",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(reader)
	if contentType != "image/png" || string(data) != "png" {
		t.Fatalf("binary body = %q, %q", contentType, data)
	}
	if _, _, err := EncodeRequestBody(context.Background(), RequestSpec{
		ContentType: "application/octet-stream",
		Body:        "data:image/png;base64",
	}, nil); err == nil || err.Error() != "data URL 格式无效" {
		t.Fatalf("invalid data URL error = %v", err)
	}
}

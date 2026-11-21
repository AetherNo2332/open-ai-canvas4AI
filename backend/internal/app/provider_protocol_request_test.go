package app

import (
	"context"
	"io"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/protocol"
)

func TestProtocolRequestFromInputUsesConfiguredBase64ForOpenAIImage(t *testing.T) {
	request := protocolRequestFromInput(canvasGenerationInput{
		Mode: "image",
		Config: providerConfig{
			InterfaceType: string(model.ChannelInterfaceOpenAIImage),
			ImageParameters: map[string]any{
				"size": "1024x1024",
			},
		},
		ImageCapability: &ImageCapabilityConfig{
			ResponseFormat: ParameterSupport{Supported: true, Default: "b64_json"},
		},
	})

	if got := request.ProviderOptions[string(model.ChannelInterfaceOpenAIImage)]["response_format"]; got != "b64_json" {
		t.Fatalf("response_format = %#v, want b64_json", got)
	}
}

func TestProtocolRequestFromInputOmitsAutomaticOpenAIImageFormat(t *testing.T) {
	request := protocolRequestFromInput(canvasGenerationInput{
		Mode: "image",
		Config: providerConfig{
			InterfaceType:   string(model.ChannelInterfaceOpenAIImage),
			ImageParameters: map[string]any{"size": "1024x1024"},
		},
		ImageCapability: &ImageCapabilityConfig{
			ResponseFormat: ParameterSupport{Supported: true, Default: "auto"},
		},
	})

	if _, ok := request.ProviderOptions[string(model.ChannelInterfaceOpenAIImage)]["response_format"]; ok {
		t.Fatal("response_format should be omitted when the model default is auto")
	}
}

func TestProtocolRequestFromInputKeepsURLForUnsupportedOpenAIImageFormat(t *testing.T) {
	request := protocolRequestFromInput(canvasGenerationInput{
		Mode:   "image",
		Config: providerConfig{InterfaceType: string(model.ChannelInterfaceOpenAIImage)},
		ImageCapability: &ImageCapabilityConfig{
			ResponseFormat: ParameterSupport{Supported: false},
		},
	})

	if _, ok := request.ProviderOptions[string(model.ChannelInterfaceOpenAIImage)]["response_format"]; ok {
		t.Fatal("response_format should be omitted when the capability is unsupported")
	}
}

func TestProtocolRequestFromInputUsesDomainRequestBodyEncoding(t *testing.T) {
	request := protocolRequestFromInput(canvasGenerationInput{
		Mode:   "text",
		Prompt: "hello",
		Config: providerConfig{Model: "future-model"},
	})
	reader, contentType, err := protocolRequestBody(context.Background(), providerConfig{}, protocol.RequestSpec{
		ContentType: "application/x-www-form-urlencoded",
		Body: map[string]any{
			"z":     "last",
			"model": request.Model,
			"a":     []any{"first", "second"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "application/x-www-form-urlencoded" || string(data) != "a=first&a=second&model=future-model&z=last" {
		t.Fatalf("request body = %q, %q", contentType, data)
	}
}

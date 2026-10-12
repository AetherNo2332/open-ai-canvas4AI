package app

import (
	"errors"
	"strings"
)

type cloudAgentVisionBatchErrorClass string

const (
	cloudAgentVisionBatchCapacity  cloudAgentVisionBatchErrorClass = "capacity"
	cloudAgentVisionBatchPermanent cloudAgentVisionBatchErrorClass = "permanent"
)

func classifyCloudAgentVisionBatchError(err error) cloudAgentVisionBatchErrorClass {
	if err == nil {
		return cloudAgentVisionBatchPermanent
	}
	text := strings.ToLower(err.Error())
	if cause := errors.Unwrap(err); cause != nil {
		text += " " + strings.ToLower(cause.Error())
	}
	for _, marker := range []string{
		"too_many_images", "too many images", "context_length_exceeded", "context length",
		"payload too large", "request too large", "vision token", "image token", "out of memory",
		"timeout", "timed out", "超出上下文", "图片数量",
	} {
		if strings.Contains(text, marker) {
			return cloudAgentVisionBatchCapacity
		}
	}
	return cloudAgentVisionBatchPermanent
}

package capability

import (
	"encoding/json"
	"reflect"
	"strings"

	"infinite-canvas/backend/internal/canvas/contract"
)

func updatedGenerationSpec(d Descriptor, node, patch map[string]any) (*contract.GenerationSpec, error) {
	if raw, exists := patch["generationSpec"]; exists {
		encoded, _ := json.Marshal(raw)
		spec, err := contract.Decode(encoded)
		return &spec, err
	}
	if d.GenerationMode == "" {
		return nil, nil
	}
	meta, _ := node["metadata"].(map[string]any)
	raw, exists := meta["generationSpec"]
	if !exists {
		return nil, nil
	}
	changed := false
	for key := range patch {
		if key == "content" || key == "model" {
			changed = true
		}
		field, ok := d.PatchFields[key]
		if !ok {
			continue
		}
		for _, nodeKey := range generationOptionNodeFields() {
			if field.Path == "metadata."+nodeKey {
				changed = true
			}
		}
	}
	if !changed {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	spec, err := contract.Decode(encoded)
	if err != nil {
		return nil, err
	}
	if text, exists := patch["content"]; exists {
		spec.Prompt = text.(string)
	}
	if value, exists := patch["model"]; exists {
		model := value.(string)
		spec.ModelSelection = nil
		if separator := strings.Index(model, "::"); separator >= 0 {
			spec.ModelSelection = &contract.ModelSelection{Kind: "channel", ChannelID: model[:separator], ModelKey: model[separator+2:]}
		}
	}
	config := spec.Options.TaskConfig()
	typ := reflect.TypeOf(contract.Options{})
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if !strings.Contains(","+field.Tag.Get("modes")+",", ","+spec.Mode+",") {
			continue
		}
		if value, exists := patch[field.Tag.Get("node")]; exists {
			if value == "" {
				delete(config, field.Tag.Get("task"))
			} else {
				config[field.Tag.Get("task")] = value
			}
		}
	}
	spec.Options, err = contract.OptionsFromTaskConfig(spec.Mode, config)
	if err != nil {
		return nil, err
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return &spec, nil
}

func generationOptionNodeFields() []string {
	typ := reflect.TypeOf(contract.Options{})
	keys := make([]string, 0, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		keys = append(keys, typ.Field(index).Tag.Get("node"))
	}
	return keys
}

func applyGenerationMirrors(node map[string]any, spec contract.GenerationSpec) {
	mirrors, _ := spec.NodeMetadata()
	metadata, _ := node["metadata"].(map[string]any)
	for _, key := range []string{"logicalModelId", "channelId", "channelModelKey", "model"} {
		delete(metadata, key)
	}
	for _, key := range generationOptionNodeFields() {
		delete(metadata, key)
	}
	for key, value := range mirrors {
		if key == "prompt" {
			continue
		}
		if number, ok := value.(int); ok {
			value = float64(number)
		}
		metadata[key] = value
	}
	metadata["generationMode"] = spec.Mode
}

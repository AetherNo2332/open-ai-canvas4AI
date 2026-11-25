package legacymigrate

import (
	"encoding/json"
	"reflect"
	"testing"
)

func shotMapDocument(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(`{
		"nodes": [
			{"id": "asset-1", "type": "image", "metadata": {"storageKey": "resource:r1", "assetId": "asset-r1", "mediaVersions": [{"id": "v1"}, {"id": "v2"}]}},
			{"id": "character-1", "type": "text", "metadata": {"workflowKind": "character", "characterAssetId": "c1", "storageKey": "resource:r2"}},
			{"id": "old-image", "type": "image", "metadata": {}},
			{"id": "old-video", "type": "video", "metadata": {}},
			{"id": "board", "type": "script", "metadata": {"storyboard": {"rows": [
				{"id": "row-1", "shotNumber": 1, "durationSeconds": 4, "plotDescription": "动作", "dialogue": "阿岚：走吧", "imageGenerationPrompt": "雨夜天台", "videoMotionPrompt": "缓慢推近", "motion": "推", "previsSceneId": "scene-1", "previsSnapshotUrl": "https://example.test/scene.png", "assetBindings": [{"nodeId": "asset-1", "role": "character", "priority": 100}, {"nodeId": "missing", "role": "style", "priority": 50}], "characters": [{"characterName": "阿岚", "characterAssetId": "c1"}], "textNodeIds": ["t"], "referenceOverrides": ["ref"], "cells": {"c": ""}},
				{"id": "row-2", "shotNumber": 2, "durationSeconds": 6, "plotDescription": "回眸", "imageGenerationPrompt": "回眸", "videoMotionPrompt": "", "imageNodeId": "old-image", "videoNodeId": "old-video", "locked": true}
			]}}}
		],
		"connections": []
	}`), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestShotSubmapsPreserveAssetsSceneAndExistingResults(t *testing.T) {
	doc := shotMapDocument(t)
	before, _ := json.Marshal(doc)
	preview, err := PreviewDocument(doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(doc)
	if string(before) != string(after) {
		t.Fatal("shot submap preview mutated the source document")
	}
	if len(preview.ShotSubmaps) != 2 {
		t.Fatalf("shot submaps = %d, want 2", len(preview.ShotSubmaps))
	}

	first := preview.ShotSubmaps[0]
	if first.SubmapID != "preview-submap-1" || first.SourceNodeID != "board" || first.RowID != "row-1" || first.ShotNumber != 1 {
		t.Fatalf("unexpected first submap identity: %+v", first)
	}
	if first.SceneID != "scene-1" || first.SceneSnapshotURL != "https://example.test/scene.png" || first.DurationSeconds != 4 {
		t.Fatalf("scene/shot fields lost: %+v", first)
	}
	if first.ExistingImage || first.ExistingVideo || first.ImageNodeID != "preview-submap-1__image-1" || first.VideoNodeID != "preview-submap-1__video-1" {
		t.Fatalf("new outputs not planned: %+v", first)
	}
	if len(first.Children) != 6 {
		t.Fatalf("children = %d, want 6", len(first.Children))
	}
	if first.Children[0].Title != "图片提示词" || first.Children[0].Content != "雨夜天台" || first.Children[1].Content != "缓慢推近" || first.Children[2].Content != "阿岚：走吧" || first.Children[3].Content != "动作" {
		t.Fatalf("prompt children lost row fields: %+v", first.Children)
	}
	if first.Children[4].Prompt != "@文本1" || !reflect.DeepEqual(first.Children[4].References, []string{first.TextNodeIDs[0]}) {
		t.Fatalf("image child not mapped to image prompt: %+v", first.Children[4])
	}
	if first.Children[5].FirstFrameID != first.ImageNodeID || first.Children[5].Seconds != "4" {
		t.Fatalf("video child start frame/duration lost: %+v", first.Children[5])
	}
	wantRefs := []string{first.TextNodeIDs[0], first.TextNodeIDs[1], first.TextNodeIDs[2], "asset-1", "missing", "character-1"}
	if !reflect.DeepEqual(first.VideoReferences, wantRefs) {
		t.Fatalf("video references = %v, want %v", first.VideoReferences, wantRefs)
	}
	if !reflect.DeepEqual(first.AssetNodeIDs, []string{"asset-1", "missing", "character-1"}) {
		t.Fatalf("asset order = %v", first.AssetNodeIDs)
	}
	if len(first.Assets) != 2 || first.Assets[0].NodeID != "asset-1" || first.Assets[0].StorageKey != "resource:r1" || first.Assets[0].AssetID != "asset-r1" || first.Assets[0].MediaVersions != 2 {
		t.Fatalf("asset versions lost: %+v", first.Assets)
	}
	if !reflect.DeepEqual(first.CharacterNodeIDs, []string{"character-1"}) {
		t.Fatalf("character association lost: %v", first.CharacterNodeIDs)
	}
	if first.Preserved["previsSceneId"] != "scene-1" || first.Preserved["assetBindings"] == nil || first.Preserved["characters"] == nil {
		t.Fatalf("preserved fields unexpected: %+v", first.Preserved)
	}

	second := preview.ShotSubmaps[1]
	if !second.ExistingImage || !second.ExistingVideo || second.ImageNodeID != "old-image" || second.VideoNodeID != "old-video" || second.FirstFrameNodeID != "old-image" {
		t.Fatalf("existing results not preserved: %+v", second)
	}
	if len(second.Children) != 4 {
		t.Fatalf("existing result row children = %d, want 4", len(second.Children))
	}
	if second.Preserved["imageNodeId"] != "old-image" || second.Preserved["videoNodeId"] != "old-video" || second.Preserved["locked"] != true {
		t.Fatalf("row results/lock lost: %+v", second.Preserved)
	}
}

func TestShotSubmapsAreDeterministicAndReadOnly(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(`{
		"nodes": [
			{"id": "character-first", "type": "text", "metadata": {"characterAssetId": "c1"}},
			{"id": "character-second", "type": "text", "metadata": {"characterAssetId": "c1"}},
			{"id": "asset-low", "type": "image", "metadata": {}},
			{"id": "asset-high", "type": "image", "metadata": {}},
			{"id": "board", "type": "script", "metadata": {"storyboard": {"rows": [
				{"id": "row-1", "shotNumber": 1, "imageGenerationPrompt": "提示词",
				 "assetBindings": [{"nodeId": "asset-low", "role": "style", "priority": 10}, {"nodeId": "asset-high", "role": "character", "priority": 100}],
				 "characters": [{"characterName": "阿岚", "characterAssetId": "c1"}]}
			]}}}
		],
		"connections": []
	}`), &doc); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(doc)
	first, err := PreviewDocument(doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := PreviewDocument(doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(doc)
	if string(before) != string(after) {
		t.Fatalf("preview mutated asset bindings: %s -> %s", before, after)
	}
	firstJSON, _ := json.Marshal(first.ShotSubmaps)
	secondJSON, _ := json.Marshal(second.ShotSubmaps)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("preview is not deterministic:\n%s\n%s", firstJSON, secondJSON)
	}
	shot := first.ShotSubmaps[0]
	if len(shot.Assets) != 2 || shot.Assets[0].NodeID != "asset-high" || shot.Assets[1].NodeID != "asset-low" {
		t.Fatalf("assets not ordered by priority: %+v", shot.Assets)
	}
	if len(shot.CharacterNodeIDs) != 1 || shot.CharacterNodeIDs[0] != "character-first" {
		t.Fatalf("character resolution must follow document order: %v", shot.CharacterNodeIDs)
	}
}

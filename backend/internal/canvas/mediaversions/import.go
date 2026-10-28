package mediaversions

import (
	"crypto/rand"
	"sort"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// Import only verifiable ready snapshots. Siblings remain independent nodes:
// importing their media never changes their positions, bindings or selection.
func importLegacyFamily(repo *repository.Repository, scope Scope, node map[string]any, sequence int64, families ...[]map[string]any) (*model.CanvasMediaVersion, int64, error) {
	meta, _ := node["metadata"].(map[string]any)
	family := str(meta["versionOfNodeId"])
	if family == "" {
		family = str(node["id"])
	}
	candidates := map[string]map[string]any{scope.NodeID: node}
	for _, nodes := range families {
		for _, sibling := range nodes {
			m, _ := sibling["metadata"].(map[string]any)
			root := str(m["versionOfNodeId"])
			if root == "" {
				root = str(sibling["id"])
			}
			if id := str(sibling["id"]); id != "" && id != scope.NodeID && root == family && sibling["type"] == node["type"] {
				candidates[id] = sibling
			}
		}
	}
	ordered := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		ordered = append(ordered, candidate)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, _ := ordered[i]["metadata"].(map[string]any)
		b, _ := ordered[j]["metadata"].(map[string]any)
		if str(a["versionLabel"]) != str(b["versionLabel"]) {
			return str(a["versionLabel"]) < str(b["versionLabel"])
		}
		return str(ordered[i]["id"]) < str(ordered[j]["id"])
	})
	var current *model.CanvasMediaVersion
	for _, candidate := range ordered {
		base, err := importReady(repo, scope, candidate, sequence+1)
		if err != nil {
			if candidate == nil || str(candidate["id"]) == scope.NodeID {
				return nil, sequence, err
			}
			if _, unavailable := err.(*Error); unavailable {
				continue
			}
			return nil, sequence, err
		}
		sequence++
		if str(candidate["id"]) == scope.NodeID {
			current = base
		}
	}
	return current, sequence, nil
}

func importReady(repo *repository.Repository, scope Scope, node map[string]any, sequence int64) (*model.CanvasMediaVersion, error) {
	m, _ := node["metadata"].(map[string]any)
	mode := str(node["type"])
	resourceID, err := readyResource(repo, scope.OwnerID, mode, m)
	if err != nil {
		return nil, err
	}
	sourceTask := str(m["taskId"])
	if sourceTask != "" {
		if _, err := repo.TaskForUser(scope.OwnerID, sourceTask); err != nil {
			sourceTask = ""
		}
	}
	var sourceVersion string
	state := ReadState(node)
	if state.CurrentVersionID != "" {
		oldScope := scope
		oldScope.NodeID = str(node["id"])
		oldScope.Epoch = state.Epoch
		oldScope.Generation = state.Generation
		if old, err := repo.CanvasMediaVersion(oldScope, state.CurrentVersionID); err == nil && old.Status == "ready" && old.ResourceID == resourceID {
			sourceVersion = old.ID
		}
	}
	base := &model.CanvasMediaVersion{ID: "mv" + rand.Text(), OwnerID: scope.OwnerID, CanvasID: scope.CanvasID, Epoch: scope.Epoch, NodeID: scope.NodeID, Generation: scope.Generation, Sequence: sequence, SourceNodeID: str(node["id"]), SourceTaskID: sourceTask, SourceVersionID: sourceVersion, Mode: mode, Status: "ready", RequestJSON: encode(map[string]any{"prompt": m["prompt"], "model": m["producedModel"]}), MediaJSON: encode(capture(m)), ResourceID: resourceID, Adoption: "imported"}
	if err = repo.InsertCanvasMediaVersion(base); err != nil {
		return nil, err
	}
	return base, nil
}

// Rebase gives restored ready media a fresh scope before the new document is
// persisted. Old task pointers are provenance only; no old pending is inherited.
func Rebase(repo *repository.Repository, scope Scope, node map[string]any) error {
	m := metadata(node)
	if !Supported(str(node["type"])) {
		delete(m, "mediaVersion")
		return nil
	}
	if _, err := readyResource(repo, scope.OwnerID, str(node["type"]), m); err != nil {
		delete(m, "mediaVersion")
		for _, key := range []string{"taskId", "generationTaskId", "taskStatus", "taskProgress", "taskStage", "taskMediaStage"} {
			delete(m, key)
		}
		if m["status"] == "loading" {
			m["status"] = "idle"
		}
		return nil
	}
	base, err := importReady(repo, scope, node, 1)
	if err != nil {
		return err
	}
	writeState(node, State{Epoch: scope.Epoch, Generation: scope.Generation, CurrentVersionID: base.ID, CurrentVersion: 1, VersionCount: 1})
	m["status"] = "success"
	return nil
}

// A deleted/restored node has a new scope. Undo can select a verifiable old
// ready result by importing its source once, never by reviving its old task.
func UseHistorical(repo *repository.Repository, scope Scope, node map[string]any, source Scope, versionID string) error {
	if scope == source {
		return Use(repo, scope, node, versionID)
	}
	if scope.OwnerID != source.OwnerID || scope.CanvasID != source.CanvasID || scope.NodeID != source.NodeID {
		return fail("version_missing", "历史版本不属于当前节点")
	}
	prior, err := PrepareSelection(repo, source, versionID)
	if err != nil {
		return err
	}
	if prior.Version.Mode != str(node["type"]) {
		return fail("target_type_mismatch", "历史媒体类型与节点不符")
	}
	imported, err := repo.CanvasMediaVersionFromSource(scope, versionID)
	if repo.IsMissing(err) {
		sequence, e := repo.CanvasMediaVersionSequence(scope)
		if e != nil {
			return e
		}
		from := prior.Version
		task := from.SourceTaskID
		if from.TaskID != nil {
			task = *from.TaskID
		}
		imported = &model.CanvasMediaVersion{ID: "mv" + rand.Text(), OwnerID: scope.OwnerID, CanvasID: scope.CanvasID, Epoch: scope.Epoch, NodeID: scope.NodeID, Generation: scope.Generation, Sequence: sequence + 1, SourceTaskID: task, SourceNodeID: source.NodeID, SourceVersionID: versionID, Mode: from.Mode, Status: "ready", RequestJSON: from.RequestJSON, MediaJSON: encode(prior.Media), OutputsJSON: from.OutputsJSON, ResourceID: from.ResourceID, Adoption: "imported"}
		if err = repo.InsertCanvasMediaVersion(imported); err != nil {
			return err
		}
		state := ReadState(node)
		state.VersionCount = imported.Sequence
		writeState(node, state)
	} else if err != nil {
		return err
	}
	return Use(repo, scope, node, imported.ID)
}

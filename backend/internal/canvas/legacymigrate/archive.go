package legacymigrate

import (
	"encoding/json"
	"fmt"
)

type Archive struct {
	ProjectID string           `json:"projectId"`
	Files     []ArchiveFile    `json:"files"`
	Drawings  []ArchiveDrawing `json:"drawings"`
}

type ArchiveFile struct {
	StorageKey string `json:"storageKey"`
	Path       string `json:"path"`
	MimeType   string `json:"mimeType,omitempty"`
	Bytes      int64  `json:"bytes,omitempty"`
}

type ArchiveDrawing struct {
	Version          int    `json:"version"`
	Engine           string `json:"engine,omitempty"`
	DrawingID        string `json:"drawingId"`
	PreviewPath      string `json:"previewPath,omitempty"`
	GenerationPath   string `json:"generationPath,omitempty"`
	SnapshotRevision int64  `json:"revision,omitempty"`
}

// PreviewZipProjects previews one project entry from a projects.json envelope.
// projectID selects a project; an empty id selects the first entry.
func PreviewZipProjects(raw []byte, projectID string, options Options) (Preview, error) {
	options.Source = SourceZip
	var envelope struct {
		Projects []struct {
			Project  map[string]any   `json:"project"`
			Files    []map[string]any `json:"files"`
			Drawings []map[string]any `json:"drawingDocuments"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Preview{}, fmt.Errorf("projects.json 格式错误: %w", err)
	}
	if len(envelope.Projects) == 0 {
		return Preview{}, fmt.Errorf("projects.json 没有项目")
	}
	selected := -1
	for index, entry := range envelope.Projects {
		if projectID == "" || text(entry.Project["id"]) == projectID {
			selected = index
			break
		}
	}
	if selected < 0 {
		return Preview{}, fmt.Errorf("projects.json 没有项目 %s", projectID)
	}
	entry := envelope.Projects[selected]
	preview, err := PreviewDocument(entry.Project, options)
	if err != nil {
		return preview, err
	}
	preview.Archive = archiveOf(entry.Project, entry.Files, entry.Drawings)
	preview.Resources = uniqueResources(append(preview.Resources, archiveResources(preview.Archive)...))
	if len(preview.Blocking) == 0 {
		preview.Blocking = append(preview.Blocking, archiveIssues(preview.Archive, entry.Project)...)
		preview.Ready = len(preview.Blocking) == 0 && len(preview.Conflicts) == 0
	}
	return preview, nil
}

func archiveOf(project map[string]any, files, drawings []map[string]any) *Archive {
	archive := &Archive{ProjectID: text(project["id"]), Files: []ArchiveFile{}, Drawings: []ArchiveDrawing{}}
	engines := map[string]string{}
	for _, node := range list(project["nodes"]) {
		metadata := object(node["metadata"])
		if id := text(metadata["drawingId"]); id != "" && text(metadata["drawingEngine"]) != "" {
			engines[id] = text(metadata["drawingEngine"])
		}
	}
	for _, file := range files {
		archive.Files = append(archive.Files, ArchiveFile{StorageKey: text(file["storageKey"]), Path: text(file["path"]), MimeType: text(file["mimeType"]), Bytes: int64(number(file["bytes"]))})
	}
	for _, drawing := range drawings {
		generation := object(drawing["generationRender"])
		engine := text(drawing["engine"])
		if engine == "" {
			engine = engines[text(drawing["drawingId"])]
		}
		archive.Drawings = append(archive.Drawings, ArchiveDrawing{
			Version: int(number(drawing["version"])), Engine: engine, DrawingID: text(drawing["drawingId"]),
			PreviewPath: text(drawing["previewPath"]), GenerationPath: text(generation["path"]), SnapshotRevision: int64(number(drawing["revision"])),
		})
	}
	return archive
}

func archiveResources(archive *Archive) []Resource {
	if archive == nil {
		return nil
	}
	out := []Resource{}
	for _, file := range archive.Files {
		out = append(out, Resource{Kind: "storage", ID: file.StorageKey, Role: file.Path})
	}
	for _, drawing := range archive.Drawings {
		out = append(out, Resource{Kind: "drawing", ID: drawing.DrawingID, Role: "snapshot"})
	}
	return out
}

func archiveIssues(archive *Archive, project map[string]any) []Issue {
	if archive == nil {
		return nil
	}
	drawings := map[string]bool{}
	for _, drawing := range archive.Drawings {
		drawings[drawing.DrawingID] = true
		if drawing.DrawingID == "" {
			return []Issue{{Code: "drawing_id_missing", Message: "drawingDocuments 条目缺少 drawingId"}}
		}
	}
	for _, node := range list(project["nodes"]) {
		if text(node["type"]) != "drawing" {
			continue
		}
		id := text(object(node["metadata"])["drawingId"])
		if id != "" && !drawings[id] {
			return []Issue{{Code: "drawing_document_missing", Path: "nodes." + text(node["id"]), Message: "绘图节点缺少 drawingDocuments 条目：" + id}}
		}
	}
	return nil
}

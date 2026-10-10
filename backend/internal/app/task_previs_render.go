package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

type previsRenderResult struct {
	ResourceID          string `json:"resourceId,omitempty"`
	PreviewResourceID   string `json:"previewResourceId,omitempty"`
	VideoNodeID         string `json:"videoNodeId,omitempty"`
	PreviewNodeID       string `json:"previewNodeId,omitempty"`
	SceneID             string `json:"sceneId"`
	ShotID              string `json:"shotId"`
	SourceHash          string `json:"sourceHash"`
	SourceTaskID        string `json:"sourceTaskId,omitempty"`
	OutputReady         bool   `json:"outputReady"`
	RendererOutputReady bool   `json:"rendererOutputReady"`
	Linked              bool   `json:"linked"`
	Width               int    `json:"width"`
	Height              int    `json:"height"`
	DurationMs          int64  `json:"durationMs"`
	FrameCount          int    `json:"frameCount"`
	RenderAttempts      int    `json:"renderAttempts"`
}

type previsRendererView struct {
	Status         string `json:"status"`
	Phase          string `json:"phase"`
	Code           string `json:"code"`
	Frames         int    `json:"frames"`
	FrameCount     int    `json:"frameCount"`
	RenderAttempts int    `json:"renderAttempts"`
	LastProgressAt int64  `json:"lastProgressAt"`
	Output         struct {
		Width      int   `json:"width"`
		Height     int   `json:"height"`
		DurationMs int64 `json:"durationMs"`
		FrameCount int   `json:"frameCount"`
		Size       int64 `json:"size"`
	} `json:"output"`
}

type previsRendererClient struct {
	base, token, owner string
	client             *http.Client
}

type previsRendererHTTPError struct {
	code   string
	status int
}

func (e previsRendererHTTPError) Error() string { return fmt.Sprintf("%s (HTTP %d)", e.code, e.status) }
func previsTransientError(err error) bool {
	var httpErr previsRendererHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.status == 408 || httpErr.status == 429 || httpErr.status >= 500
	}
	return err != nil && err.Error() == "renderer_unavailable"
}

func newPrevisRendererClient(owner string) (*previsRendererClient, error) {
	base := strings.TrimRight(os.Getenv("CANVAS_PREVIS_RENDERER_URL"), "/")
	parsed, err := url.Parse(base)
	token := os.Getenv("CANVAS_PREVIS_RENDERER_TOKEN")
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || len(token) < 32 {
		return nil, fmt.Errorf("renderer_not_configured")
	}
	return &previsRendererClient{base: base, token: token, owner: owner, client: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *previsRendererClient) request(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Previs-Lease-Owner", c.owner)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("renderer_unavailable")
	}
	if res.StatusCode/100 != 2 {
		defer res.Body.Close()
		var detail struct {
			Code string `json:"code"`
		}
		json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&detail)
		code := detail.Code
		if code == "" || strings.ContainsAny(code, " /:\n") {
			code = "renderer_http_error"
		}
		return nil, previsRendererHTTPError{code: code, status: res.StatusCode}
	}
	return res, nil
}
func (c *previsRendererClient) json(ctx context.Context, method, path string, body any) (previsRendererView, error) {
	var raw []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return previsRendererView{}, err
		}
		raw = encoded
	}
	var res *http.Response
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		res, err = c.request(ctx, method, path, bytes.NewReader(raw))
		if err == nil || !previsTransientError(err) || attempt == 2 {
			break
		}
		select {
		case <-ctx.Done():
			return previsRendererView{}, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 500 * time.Millisecond):
		}
	}
	if err != nil {
		return previsRendererView{}, err
	}
	defer res.Body.Close()
	var view previsRendererView
	err = json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&view)
	return view, err
}

func (w *taskWorkerCoordinator) processPrevisRender(task *model.Task, ctx context.Context) error {
	s := w.service
	user, userErr := s.repo.User(task.UserID)
	if userErr != nil || user.Status != model.UserStatusActive {
		return s.failPrevis(task, "permission_denied", "prepare")
	}
	var input previsRenderInput
	if json.Unmarshal([]byte(task.InputJSON), &input) != nil || input.CanvasID == "" || input.SourceHash == "" {
		return s.failPrevis(task, "invalid_scene", "prepare")
	}
	var result previsRenderResult
	_ = json.Unmarshal([]byte(task.ResultJSON), &result)
	if input.RepairSourceTaskID != "" {
		source, err := s.repo.TaskForUser(task.UserID, input.RepairSourceTaskID)
		if err != nil || source.Type != model.TaskTypePrevisRender || source.Status != model.TaskStatusFailed {
			return s.failPrevis(task, "invalid_repair", "prepare")
		}
		if json.Unmarshal([]byte(source.ResultJSON), &result) != nil || (!result.OutputReady && !result.RendererOutputReady) {
			return s.failPrevis(task, "missing_resource", "prepare")
		}
		result.SourceTaskID = source.ID
		if err := s.checkpointPrevisOutput(task, result); err != nil {
			return err
		}
	}
	if result.OutputReady && result.ResourceID != "" && result.PreviewResourceID != "" {
		if err := s.commitPrevisOutput(task, input, result); err != nil {
			return s.failPrevis(task, previsErrorCode(err), "writeback")
		}
		return nil
	}
	client, err := newPrevisRendererClient(task.LeaseOwner)
	if err != nil {
		return s.failPrevis(task, "renderer_not_configured", "prepare")
	}
	renderTaskID := task.ID
	if input.RepairSourceTaskID != "" {
		renderTaskID = input.RepairSourceTaskID
	}
	path := "/jobs/" + url.PathEscape(renderTaskID)
	rendererTaskID, rendererUserID, rendererOwner := task.ID, task.UserID, task.LeaseOwner
	// The backend's own task lease must still be valid before granting a renderer lease.
	renew := func() error {
		latest, err := s.repo.TaskForUser(rendererUserID, rendererTaskID)
		if err != nil {
			return err
		}
		if latest.Status != model.TaskStatusRunning || latest.LeaseOwner != rendererOwner || (latest.LeaseExpiresAt != nil && !latest.LeaseExpiresAt.After(time.Now())) {
			return repository.ErrTaskStateConflict
		}
		_, err = client.json(ctx, http.MethodPost, path+"/renew", map[string]any{"leaseUntil": time.Now().Add(40 * time.Second).UnixMilli()})
		return err
	}
	renderInput := input
	renderInput.RepairMode = ""
	renderInput.RepairSourceTaskID = ""
	renderAttempt := max(1, task.Attempts)
	if input.RepairSourceTaskID != "" {
		source, sourceErr := s.repo.TaskForUser(task.UserID, input.RepairSourceTaskID)
		if sourceErr != nil {
			return s.failPrevis(task, "invalid_repair", "prepare")
		}
		renderAttempt = max(renderAttempt, source.Attempts+1)
	}
	body := map[string]any{"taskId": renderTaskID, "attempt": renderAttempt, "leaseOwner": task.LeaseOwner, "leaseUntil": time.Now().Add(40 * time.Second).UnixMilli(), "input": renderInput, "recoverOnly": input.RepairSourceTaskID != ""}
	var view previsRendererView
	leaseWaitDeadline := time.Now().Add(45 * time.Second)
	for {
		latest, readErr := s.repo.TaskForUser(task.UserID, task.ID)
		if readErr != nil || latest.Status != model.TaskStatusRunning || latest.LeaseOwner != task.LeaseOwner || latest.LeaseExpiresAt == nil || !latest.LeaseExpiresAt.After(time.Now()) {
			return repository.ErrTaskStateConflict
		}
		body["leaseUntil"] = time.Now().Add(40 * time.Second).UnixMilli()
		view, err = client.json(ctx, http.MethodPost, "/jobs", body)
		if err == nil {
			break
		}
		// The old renderer lease can briefly outlive its backend worker lease.
		// Wait for expiry without replacing an active owner or extending our authority.
		var leaseErr previsRendererHTTPError
		if !errors.As(err, &leaseErr) || leaseErr.code != "lease_lost" || leaseErr.status != http.StatusConflict || time.Now().After(leaseWaitDeadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if err != nil {
		return s.failPrevis(task, previsErrorCode(err), "submit")
	}
	// Renew throughout uploads, encoding and downloads, not only the polling loop.
	executionCtx, cancelExecution := context.WithCancel(ctx)
	defer cancelExecution()
	ctx = executionCtx
	renewDone := make(chan struct{})
	defer func() {
		close(renewDone)
		// Only release renderer copies after both owned resources have a durable checkpoint.
		if result.OutputReady {
			ackCtx, cancelAck := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelAck()
			_, _ = client.json(ackCtx, http.MethodPost, path+"/ack", nil)
		}
	}()
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-renewDone:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if renew() != nil {
					cancelExecution()
					return
				}
			}
		}
	}()
	defer func() {
		latest, readErr := s.repo.Task(task.ID)
		if readErr == nil && latest.Status == model.TaskStatusCancelled {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = client.json(cancelCtx, http.MethodPost, path+"/cancel", nil)
		}
	}()
	if view.Status == "preparing" {
		var total int64
		for name, id := range input.Assets {
			resource, reader, openErr := s.OpenResource(task.UserID, id)
			if openErr != nil {
				return s.failPrevis(task, "missing_resource", "assets")
			}
			total += resource.Size
			if resource.Status != model.ResourceStatusReady || resource.Size <= 0 || total > 64<<20 {
				reader.Close()
				return s.failPrevis(task, "invalid_asset", "assets")
			}
			res, uploadErr := client.request(ctx, http.MethodPut, path+"/assets/"+url.PathEscape(name), io.LimitReader(reader, resource.Size+1))
			reader.Close()
			if res != nil {
				res.Body.Close()
			}
			if uploadErr != nil {
				return s.failPrevis(task, previsErrorCode(uploadErr), "assets")
			}
			if err = renew(); err != nil {
				return err
			}
		}
		view, err = client.json(ctx, http.MethodPost, path+"/start", nil)
		if err != nil {
			return s.failPrevis(task, previsErrorCode(err), "submit")
		}
	}
	lastPhase := ""
	lastFrames := -1
	progressAt := time.Now()
	for view.Status != "output_ready" {
		if view.Status == "failed" || view.Status == "cancelled" {
			return s.failPrevis(task, view.Code, "render")
		}
		if view.Phase != lastPhase || view.Frames != lastFrames {
			lastPhase, lastFrames = view.Phase, view.Frames
			progressAt = time.Now()
			progress := 0
			if view.FrameCount > 0 {
				progress = min(80, view.Frames*80/view.FrameCount)
			}
			if err = w.progress(task, "预演："+view.Phase, progress); err != nil {
				return err
			}
		}
		if time.Since(progressAt) > 5*time.Minute {
			return s.failPrevis(task, "renderer_stalled", "render")
		}
		select {
		case <-ctx.Done():
			return s.failPrevis(task, "execution_interrupted", "render")
		case <-time.After(time.Second):
		}
		view, err = client.json(ctx, http.MethodGet, path, nil)
		if err != nil {
			return s.failPrevis(task, previsErrorCode(err), "poll")
		}
	}
	if err = renew(); err != nil {
		return err
	}
	result = previsRenderResult{SceneID: input.SceneID, ShotID: input.ShotID, SourceHash: input.SourceHash, Width: input.Width, Height: input.Height, RenderAttempts: view.RenderAttempts, RendererOutputReady: true, SourceTaskID: input.RepairSourceTaskID}
	if err = s.checkpointPrevisOutput(task, result); err != nil {
		return err
	}
	workDir, err := os.MkdirTemp(filepath.Join(s.dataDir), "previs-output-")
	if err != nil {
		return s.failPrevis(task, "storage_unavailable", "download")
	}
	defer os.RemoveAll(workDir)
	// Stable upload identity reconciles a crash between registration and checkpoint.
	for _, kind := range []string{"video", "preview"} {
		filePath := filepath.Join(workDir, kind)
		if err = client.download(ctx, path+"/"+kind, filePath, 128<<20); err != nil {
			return s.failPrevis(task, previsErrorCode(err), "download")
		}
		if kind == "video" {
			if err = probePrevisVideo(ctx, filePath, input, &result); err != nil {
				return s.failPrevis(task, "invalid_output", "probe")
			}
		} else {
			file, _ := os.Open(filePath)
			config, _, probeErr := image.DecodeConfig(file)
			file.Close()
			if probeErr != nil || config.Width != input.Width || config.Height != input.Height {
				return s.failPrevis(task, "invalid_preview", "probe")
			}
		}
		file, _ := os.Open(filePath)
		stat, _ := file.Stat()
		mime := "video/mp4"
		resourceKind := "media"
		duration := result.DurationMs
		if kind == "preview" {
			mime = "image/png"
			resourceKind = "image"
			duration = 0
		}
		key := "previs:" + renderTaskID + ":" + kind
		resource, saveErr := s.storePrevisArtifact(ctx, task, resourceKind, "previs-"+kind, mime, stat.Size(), input.Width, input.Height, duration, file, key)
		file.Close()
		if saveErr != nil {
			code := "storage_failed"
			if previsErrorCode(saveErr) == "quota_exceeded" {
				code = "quota_exceeded"
			}
			return s.failPrevis(task, code, "save")
		}
		if kind == "video" {
			result.ResourceID = resource.ID
		} else {
			result.PreviewResourceID = resource.ID
			result.OutputReady = true
		}
		if err = s.checkpointPrevisOutput(task, result); err != nil {
			return err
		}
	}
	if err = s.commitPrevisOutput(task, input, result); err != nil {
		return s.failPrevis(task, previsErrorCode(err), "writeback")
	}
	return nil
}

func (s *Service) storePrevisArtifact(ctx context.Context, task *model.Task, kind, name, mime string, size int64, width, height int, duration int64, file *os.File, key string) (*model.Resource, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		latest, err := s.repo.TaskForUser(task.UserID, task.ID)
		if err != nil {
			return nil, err
		}
		if latest.Status != model.TaskStatusRunning || latest.LeaseOwner != task.LeaseOwner {
			return nil, repository.ErrTaskStateConflict
		}
		existing, err := s.repo.ResourceByUploadKey(task.UserID, key)
		if err == nil {
			if existing.MimeType != mime || existing.Size != size || existing.Width != width || existing.Height != height {
				return nil, fmt.Errorf("resource_identity_conflict")
			}
			if existing.Status == model.ResourceStatusReady {
				return existing, nil
			}
			day, quotaErr := s.reserveGeneratedResourceQuota(task.UserID, size)
			if quotaErr != nil {
				return nil, quotaErr
			}
			file.Seek(0, io.SeekStart)
			etag, writeErr := s.storeResourceObject(existing, name, file)
			if writeErr == nil {
				existing.Status = model.ResourceStatusReady
				existing.ETag = etag
				existing.Error = ""
				existing.UpdatedAt = time.Now()
				writeErr = s.repo.SaveResource(existing)
				if writeErr == nil {
					s.commitUserUploadQuota(task.UserID, size)
					return existing, nil
				}
			}
			s.releaseUserUploadQuota(task.UserID, day, size)
			lastErr = writeErr
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			day, quotaErr := s.reserveGeneratedResourceQuota(task.UserID, size)
			if quotaErr != nil {
				return nil, quotaErr
			}
			file.Seek(0, io.SeekStart)
			resource, _, saveErr := s.storeResource(task.UserID, kind, name, mime, size, width, height, duration, file, &key, false)
			if saveErr == nil {
				s.commitUserUploadQuota(task.UserID, size)
				return resource, nil
			}
			s.releaseUserUploadQuota(task.UserID, day, size)
			lastErr = saveErr
		} else {
			lastErr = err
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(1+attempt*2) * time.Second):
			}
		}
	}
	return nil, lastErr
}

func (c *previsRendererClient) download(ctx context.Context, path, destination string, limit int64) error {
	res, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.ContentLength > limit {
		return fmt.Errorf("output_too_large")
	}
	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(file, io.LimitReader(res.Body, limit+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || n <= 0 || n > limit {
		return fmt.Errorf("invalid_output")
	}
	return nil
}
func probePrevisVideo(ctx context.Context, path string, input previsRenderInput, result *previsRenderResult) error {
	binary := os.Getenv("CANVAS_FFPROBE_PATH")
	if binary == "" {
		binary = "ffprobe"
	}
	raw, err := exec.CommandContext(ctx, binary, "-v", "error", "-select_streams", "v:0", "-count_frames", "-show_entries", "stream=codec_name,width,height,nb_read_frames:format=duration", "-of", "json", path).Output()
	if err != nil {
		return err
	}
	var probe struct {
		Streams []struct {
			Codec  string `json:"codec_name"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
			Frames string `json:"nb_read_frames"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if json.Unmarshal(raw, &probe) != nil || len(probe.Streams) != 1 {
		return fmt.Errorf("invalid_output")
	}
	var frames int
	var duration float64
	fmt.Sscan(probe.Streams[0].Frames, &frames)
	fmt.Sscan(probe.Format.Duration, &duration)
	if probe.Streams[0].Codec != "h264" || probe.Streams[0].Width != input.Width || probe.Streams[0].Height != input.Height || frames != int(math.Ceil(input.Duration*float64(input.FPS))) || math.Abs(duration-float64(frames)/float64(input.FPS)) > 0.15 {
		return fmt.Errorf("invalid_output")
	}
	result.DurationMs = int64(math.Round(duration * 1000))
	result.FrameCount = frames
	return nil
}

func (s *Service) checkpointPrevisOutput(task *model.Task, result previsRenderResult) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if err = s.repo.SavePrevisCheckpoint(task, string(raw)); err != nil {
		return err
	}
	task.ResultJSON = string(raw)
	ids := []string{result.ResourceID, result.PreviewResourceID}
	return s.repo.UpsertCloudAgentResourceLeases(task.UserID, task.AgentRunID, "previs-task:"+task.ID, ids, time.Now().Add(24*time.Hour))
}

func (s *Service) commitPrevisOutput(task *model.Task, input previsRenderInput, result previsRenderResult) error {
	policy, err := s.RuntimePolicy()
	if err != nil {
		return err
	}
	origin := task.ID
	if result.SourceTaskID != "" {
		origin = result.SourceTaskID
	}
	result.VideoNodeID = cloudAgentID(task.UserID, origin+":previs-video")
	result.PreviewNodeID = cloudAgentID(task.UserID, origin+":previs-preview")
	result.Linked = input.RepairMode != "independent"
	raw, _ := json.Marshal(result)
	completed := *task
	completed.Status = model.TaskStatusSucceeded
	completed.ResultJSON = string(raw)
	completed.Progress = 100
	completed.Stage = "预演已保存并写回画布"
	completed.CompletedAt = ptr(time.Now())
	completed.LeaseOwner = task.LeaseOwner
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	for attempt := 0; attempt < 3; attempt++ {
		err = s.repo.SaveTaskCompletionWithRegistration(&completed, model.TaskStatusRunning, nil, func(repo *repository.Repository) error {
			user, userErr := repo.User(task.UserID)
			if userErr != nil || user.Status != model.UserStatusActive {
				return fmt.Errorf("permission_denied")
			}
			if task.AgentRunID != "" {
				run, readErr := repo.CloudAgent(task.UserID, task.AgentRunID)
				if readErr != nil {
					return readErr
				}
				if run.Status == "cancelled" {
					return fmt.Errorf("user_cancelled")
				}
			}
			for index, id := range []string{result.ResourceID, result.PreviewResourceID} {
				resource, readErr := repo.ResourceForUser(task.UserID, id)
				mime := "video/mp4"
				if index == 1 {
					mime = "image/png"
				}
				if readErr != nil || resource.Status != model.ResourceStatusReady || resource.MimeType != mime || resource.Width != result.Width || resource.Height != result.Height || resource.Size < 100 {
					return fmt.Errorf("missing_resource")
				}
			}
			canvas, readErr := repo.CanvasProjectForUser(task.UserID, input.CanvasID)
			if readErr != nil {
				return fmt.Errorf("target_unavailable")
			}
			doc, readErr := creationDocument(canvas.PayloadJSON)
			if readErr != nil {
				return fmt.Errorf("invalid_canvas")
			}
			cloudAgentPrevisRepairLegacyNodes(doc)
			scene, exists := findPrevisScene(creationMaps(doc["previsScenes"]), input.SceneID)
			if result.Linked && (!exists || previsRenderSourceHash(scene, input.ShotID) != input.SourceHash) {
				return fmt.Errorf("target_changed")
			}
			nodes := creationMaps(doc["nodes"])
			workstationFound := false
			for _, node := range nodes {
				metadata, _ := node["metadata"].(map[string]any)
				if stringValue(node["type"]) == "video" && stringValue(metadata["workflowKind"]) == "shot" && stringValue(metadata["previsSceneId"]) == input.SceneID {
					workstationFound = true
					break
				}
			}
			if !workstationFound {
				return fmt.Errorf("previs_workstation_missing sceneId=%s; use previs_scene_create or previs_apply_patch to repair the canvas", input.SceneID)
			}
			for _, output := range []struct {
				id, resource, kind, workflowKind, title string
				x                                       float64
			}{{result.VideoNodeID, result.ResourceID, "video", "reference_video", "白模预演", 0}, {result.PreviewNodeID, result.PreviewResourceID, "image", "reference_set", "预演构图帧", 380}} {
				found := false
				for _, node := range nodes {
					if stringValue(node["id"]) == output.id {
						found = true
						metadata, _ := node["metadata"].(map[string]any)
						if stringValue(node["type"]) != output.kind || stringValue(metadata["storageKey"]) != "resource:"+output.resource || stringValue(metadata["content"]) != resourceFileURL(output.resource) || stringValue(metadata["previsSourceHash"]) != input.SourceHash {
							return fmt.Errorf("node_conflict")
						}
					}
				}
				if found {
					continue
				}
				metadata := map[string]any{"content": resourceFileURL(output.resource), "storageKey": "resource:" + output.resource, "status": "success", "taskId": origin, "taskStatus": "succeeded", "workflowKind": output.workflowKind, "assetTags": []any{"预演台" + map[bool]string{true: "构图", false: "白膜"}[output.workflowKind == "reference_set"], "镜头:" + input.ShotID}, "previsSceneId": input.SceneID, "previsShotId": input.ShotID, "previsSourceHash": input.SourceHash, "previsIndependent": !result.Linked, "naturalWidth": result.Width, "naturalHeight": result.Height, "duration": float64(result.DurationMs) / 1000, "previsRepairTaskId": task.ID}
				y := float64(len(nodes)/2) * 300
				nodes = append(nodes, creationAddedNode(CreationCanvasOp{ID: output.id, NodeType: output.kind, Title: output.title, X: ptr(output.x), Y: ptr(y), Metadata: metadata}))
			}
			doc["nodes"] = nodes
			if result.Linked {
				shot, _ := findPrevisShot(scene, input.ShotID)
				shot["previewNodeId"], shot["clayVideoNodeId"] = result.PreviewNodeID, result.VideoNodeID
			}
			payload, encodeErr := json.Marshal(doc)
			if encodeErr != nil {
				return encodeErr
			}
			if validateSyncedPayload(payload, "画布") != nil {
				return fmt.Errorf("payload_too_large")
			}
			if err := saveCloudAgentDocument(repo, canvas, doc, policy); err != nil {
				return err
			}
			return repo.ReleaseCloudAgentResourceLeases(task.UserID, "previs-task:"+task.ID)
		})
		if !errors.Is(err, repository.ErrCreationConflict) {
			break
		}
	}
	if err == nil {
		*task = completed
	}
	return err
}

func previsErrorCode(err error) string {
	var publicError *AppError
	if errors.As(err, &publicError) && string(publicError.Reason) == "quota_exceeded" {
		return "quota_exceeded"
	}
	if errors.Is(err, repository.ErrCreationConflict) {
		return "canvas_write_conflict"
	}
	if errors.Is(err, repository.ErrTaskStateConflict) {
		return "lease_lost"
	}
	code := strings.Split(err.Error(), " ")[0]
	if len(code) > 80 || strings.ContainsAny(code, "/:\n") || strings.IndexFunc(code, func(r rune) bool { return r > 127 }) >= 0 {
		return "storage_unavailable"
	}
	return code
}
func (s *Service) failPrevis(task *model.Task, code, phase string) error {
	if code == "" {
		code = "renderer_error"
	}
	task.Status = model.TaskStatusFailed
	task.Stage = "预演未交付：" + phase
	task.Error = code
	diagnostic := model.TaskExecutionDiagnostic{Code: code, Phase: phase, TaskSubmitted: true, SafeMessage: "后台预演未完成；已保存产物可从任务读取，不会自动重复渲染"}
	raw, _ := json.Marshal(diagnostic)
	task.ExecutionDiagnosticJSON = string(raw)
	_, err := s.repo.UpdateTaskTerminalDiagnostic(task, time.Now())
	return err
}

// A repair is a new, idempotent operation. The failed original remains unchanged.
func (s *Service) cancelRunPrevis(ctx context.Context, userID, runID string) error {
	tasks, err := s.repo.PendingPrevisForRun(userID, runID)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if _, err = s.taskLifecycle().cancelTaskWithIntent(ctx, userID, task.ID, model.TaskCancellationIntent{Source: model.TaskCancellationParentCancelled, ActorID: userID}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) RecoverPrevisWriteback(userID, id, mode string) (*model.Task, error) {
	if mode != "link" && mode != "independent" {
		return nil, BadAuthRequest("请选择link或independent恢复方式")
	}
	source, err := s.repo.TaskForUser(userID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFound("预演任务不存在")
		}
		return nil, err
	}
	if source.Type != model.TaskTypePrevisRender || source.Status != model.TaskStatusFailed {
		return nil, BadAuthRequest("仅能恢复失败预演的已保存产物")
	}
	var input previsRenderInput
	var result previsRenderResult
	if json.Unmarshal([]byte(source.InputJSON), &input) != nil || json.Unmarshal([]byte(source.ResultJSON), &result) != nil || (!result.OutputReady && !result.RendererOutputReady) {
		return nil, BadAuthRequest("任务没有可恢复产物")
	}
	// A failed recovery still refers to the same original render and artifact identities.
	if input.RepairSourceTaskID != "" {
		if input.RepairSourceTaskID == id {
			return nil, BadAuthRequest("恢复来源无效")
		}
		return s.RecoverPrevisWriteback(userID, input.RepairSourceTaskID, mode)
	}
	input.RepairSourceTaskID = id
	input.RepairMode = mode
	if _, err = s.repo.CanvasProjectForUser(userID, input.CanvasID); err != nil {
		return nil, err
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(input)
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	var repairID string
	for attempt := 0; attempt < 8; attempt++ {
		repairID = cloudAgentID(userID, fmt.Sprintf("%s:previs-repair:%s:%d", id, mode, attempt))
		existing, lookupErr := s.repo.TaskForUser(userID, repairID)
		if errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			break
		}
		if lookupErr != nil {
			return nil, lookupErr
		}
		if existing.Status != model.TaskStatusFailed && existing.Status != model.TaskStatusCancelled {
			return taskForOutput(*existing), nil
		}
		if attempt == 7 {
			return nil, BadAuthRequest("此方式已恢复8次，请检查诊断后创建新的授权预演")
		}
	}
	task := &model.Task{ID: repairID, UserID: userID, ProjectID: input.CanvasID, Type: model.TaskTypePrevisRender, Status: model.TaskStatusQueued, Stage: "等待只回写恢复", Provider: "local", Model: "previs-writeback", InputJSON: string(raw)}
	err = createTaskWithStorageQuotaRepository(s.repo, task, nil, policy)
	if err != nil {
		if existing, readErr := s.repo.TaskForUser(userID, repairID); readErr == nil {
			return taskForOutput(*existing), nil
		}
		return nil, err
	}
	s.wakeTaskDispatcher()
	return taskForOutput(*task), nil
}

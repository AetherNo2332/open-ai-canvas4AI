package app

import (
	"encoding/json"
	"maps"
	"regexp"
	"strings"

	"yingce/backend/internal/kernel"
)

type agentReadAdapter struct {
	namespace string
	business  string
	version   int
	vision    bool
	read      func(*Service, string, *cloudAgentRuntime, string, agentReadArguments, cloudAgentCall) (agentReadResult, error)
}

type agentReadResult struct {
	fields map[string]any
	image  *cloudAgentImageInspection
}

func builtinAgentReadAdapters() []agentReadAdapter {
	return []agentReadAdapter{
		{namespace: "canvas:text", business: "canvas", version: 1, read: (*Service).agentReadCanvasText},
		{namespace: "canvas:image", business: "canvas", version: 1, vision: true, read: (*Service).agentReadCanvasImage},
		{namespace: "drama:episode", business: "drama", version: 1, read: (*Service).agentReadDramaEpisode},
	}
}

type agentReadArguments struct {
	Path   string `json:"path"`
	Offset *int   `json:"offset"`
	Limit  *int   `json:"limit"`
}

func (args agentReadArguments) offset() int {
	if args.Offset != nil {
		return *args.Offset
	}
	return 0
}

func (args agentReadArguments) limit() int {
	if args.Limit != nil {
		return *args.Limit
	}
	return piNativeSkillReadMaxRunes
}

func agentReadArgumentsFor(call cloudAgentCall) (agentReadArguments, error) {
	var args agentReadArguments
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return args, cloudAgentJSONArgumentError(err)
	}
	if args.offset() < 0 || args.limit() < 1 || args.limit() > piNativeSkillReadMaxRunes {
		return args, BadAuthRequest("read 分页范围无效")
	}
	return args, nil
}

var agentBusinessReadPrefix = regexp.MustCompile(`^[a-z][a-z0-9_-]+:`)
var agentReadNamespace = regexp.MustCompile(`^[a-z][a-z0-9_-]+:[a-z][a-z0-9_-]*$`)

func agentBusinessRead(call cloudAgentCall) bool {
	if call.Function.Name != "read" {
		return false
	}
	var args struct {
		Path string `json:"path"`
	}
	return json.Unmarshal([]byte(call.Function.Arguments), &args) == nil && agentBusinessReadPrefix.MatchString(args.Path)
}

func (s *Service) agentReadBusinessContent(userID string, state *cloudAgentRuntime, call cloudAgentCall) (any, error) {
	args, err := agentReadArgumentsFor(call)
	if err != nil {
		return nil, err
	}
	namespace, objectID, found := strings.Cut(args.Path, "/")
	if !found || !agentReadNamespace.MatchString(namespace) || strings.ContainsAny(objectID, `/\:%?#`) ||
		objectID == "." || objectID == ".." || validateCloudAgentID(objectID, "read 对象 ID", 80) != nil {
		return nil, BadAuthRequest("read 需要业务命名空间与单一对象 ID")
	}
	scope := agentBusinessScope(state.Request)
	adapter := s.agentDefinitionRegistry().readAdapters[namespace]
	if adapter.read == nil || adapter.business != scope.Type ||
		(state.Capabilities != nil && state.Capabilities.ReadAdapterVersions[namespace] != adapter.version) {
		return nil, kernel.Forbidden("read 引用未获本轮业务授权或冻结适配器版本不可用")
	}
	if adapter.vision {
		if !state.Request.VisionEnabled {
			return nil, kernel.Forbidden("本轮模型不支持图片输入")
		}
		if args.Offset != nil || args.Limit != nil {
			return nil, BadAuthRequest("图片 read 不接受文本分页")
		}
	}
	result, err := adapter.read(s, userID, state, objectID, args, call)
	if err != nil {
		return nil, err
	}
	receipt := map[string]any{"source": args.Path, "visibility": "authorized", "contentType": "text/plain"}
	maps.Copy(receipt, result.fields)
	if result.image == nil {
		return receipt, nil
	}
	receipt["contentType"] = "image"
	maps.Copy(receipt, result.image.Receipt)
	result.image.Receipt = receipt
	return *result.image, nil
}

func (s *Service) agentReadCanvasText(userID string, state *cloudAgentRuntime, nodeID string, args agentReadArguments, call cloudAgentCall) (agentReadResult, error) {
	payload, _ := json.Marshal(map[string]any{"nodeId": nodeID, "offset": args.offset(), "limit": args.limit()})
	call.Function.Name, call.Function.Arguments = "canvas_read_content", string(payload)
	result, err := cloudAgentContentRead(s.repo, userID, state.Request.CanvasID, call)
	fields, _ := result.(map[string]any)
	return agentReadResult{fields: fields}, err
}

func (s *Service) agentReadCanvasImage(userID string, state *cloudAgentRuntime, nodeID string, _ agentReadArguments, call cloudAgentCall) (agentReadResult, error) {
	payload, _ := json.Marshal(map[string]any{"nodeId": nodeID, "refresh": true})
	call.Function.Name, call.Function.Arguments = "canvas_inspect_image", string(payload)
	result, err := s.prepareCloudAgentImageInspection(userID, state.Request.CanvasID, state, call)
	if err != nil {
		return agentReadResult{}, err
	}
	inspection, ok := result.(cloudAgentImageInspection)
	if !ok || inspection.ImageURL == "" {
		return agentReadResult{}, kernel.BadAuthRequest("本次图片未附送：已达到图片读取上限，请使用已收到的图片或摘要")
	}
	return agentReadResult{image: &inspection}, nil
}

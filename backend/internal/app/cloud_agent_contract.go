package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// 首步合同（路线图 P1.5）把"这一轮用什么提示、什么工具、多少钱"从"worker 进程里的内存"
// 提升成"运行记录里的不可变事实"。三件事必须一起成立，缺一个就会重新出现迁移前的漏洞：
//
//  1. 显式合同版本与阶段：版本 2 的运行在建 run 之后、首个模型步之前处于
//     awaiting_first_step —— 只有这个阶段允许空任务历史，其他情况一律判损坏。
//  2. 不可变快照：服务端策略正文、Harness 正文与工具 schema 摘要一次固化，
//     恢复（重启、换 worker）时用快照继续，而不是重读当时的磁盘文件或工具目录。
//  3. 占位预留：建 run 用一张**不可领取**的占位任务承载报价预留，首个 PiModelStep
//     在同一事务里重新报价并换成真实首步任务 —— 报价与首步之间不再有"没记账的窗口"。
const (
	// cloudAgentContractVersionFirstStep 是本轮引入的合同版本。
	// 缺省 0/1 是迁移前的合同：建 run 时提交可执行的根模型任务，Go 编译的提示被当成首步提示，
	// 恢复只能重读当时的配置。版本 2 起，首步由 Node 的最终提示驱动。
	cloudAgentContractVersionFirstStep = 2

	// cloudAgentPhaseAwaitingFirstStep 是版本 2 运行在"建 run 之后、首个模型步之前"的唯一合法阶段。
	cloudAgentPhaseAwaitingFirstStep = "awaiting_first_step"

	// cloudAgentHoldingOperation 是占位任务的操作名。
	//
	// 它必须与 cloudAgentOperation 区分开：读路径用 `operation = cloud_agent` 认定
	// "这一行是旧版根任务"，而占位任务不是可续跑的根任务。若沿用同一个操作名，
	// 换单之后残留的占位行会被当成根任务，运行状态就会跟着占位行的终态走。
	// 用独立操作名之后，"有占位行的新 run"一律以执行记录为权威，旧 run 行为逐字段不变。
	cloudAgentHoldingOperation = "cloud_agent_holding"

	// cloudAgentContractSnapshotVersion 是快照自身的结构版本，便于以后增量扩展。
	cloudAgentContractSnapshotVersion = 1
)

// cloudAgentHarnessPart 是 Harness 上下文文件的一段正文（文件名 + 内容）。
type cloudAgentHarnessPart struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

// cloudAgentHarnessSnapshot 是 Node 装配提示所用的 Harness 正文快照。
//
// 字段名与 agent/src/system-prompt.ts 的 PromptParts 一一对应：Node 在首个模型步
// 提交它，Go 固化后通过运行快照回发给任何后续 worker。恢复的 worker 因此不必重读
// 磁盘上的 Harness 文件 —— 运维改了文件再重启，也不会静默换掉在途运行的系统提示。
type cloudAgentHarnessSnapshot struct {
	System       *string                `json:"system,omitempty"`
	AppendSystem *string                `json:"appendSystem,omitempty"`
	// Context 不带 omitempty：Node 侧的 PromptParts.context 是必填数组，省略后会变成
	// undefined，装配与哈希都会抛错。没有上下文文件时发空数组才是同一份合同。
	Context []cloudAgentHarnessPart `json:"context"`
}

// cloudAgentContractSnapshot 是一次运行的不可变提示合同。
//
// 它只增不改：首个模型步写入 Harness 正文与哈希之后，后续步骤只能与之比对。
type cloudAgentContractSnapshot struct {
	Version int `json:"version"`
	// ServerPolicyHash 是服务端策略正文的摘要。
	//
	// 正文本身就在 state.Canonical.SystemPrompt 里（同一份状态 JSON，本来就要持久化，
	// 也是 Node 恢复时实际收到的策略）。这里再存一份正文只会把同一段 30KB 级文本
	// 抄两遍，所以快照存的是它的**不可变身份**：策略一旦被谁改写，比对立刻失败。
	ServerPolicyHash string `json:"serverPolicyHash"`
	// HarnessHash / Harness 在首个模型步固化（Node 是 Harness 的唯一装配方）。
	HarnessHash string                     `json:"harnessHash,omitempty"`
	Harness     *cloudAgentHarnessSnapshot `json:"harness,omitempty"`
	// AssembledPromptHash 是首个模型步实际发出的那份系统提示（服务端策略 + Harness 装配
	// 结果）的内容身份，按去首尾空白后计算。
	//
	// 为什么在策略哈希与 Harness 哈希之外还要这一条：前两条只证明"输入没被换过"，
	// 这一条证明"Node 的装配函数把同一份输入渲染成了同一段提示"。少了它，
	// 一个装配函数的改动（或篡改）可以让恢复后的第二步拿着另一段提示去请求模型，
	// 而所有输入哈希都还是对的。
	AssembledPromptHash string `json:"assembledPromptHash,omitempty"`
	// 工具 schema 的不可变身份：名字集合 + 参数摘要。描述会随披露动态改写，不能进摘要。
	ToolSchemaVersion string   `json:"toolSchemaVersion,omitempty"`
	ToolNames         []string `json:"toolNames,omitempty"`
	ToolsDigest       string   `json:"toolsDigest,omitempty"`
}

// cloudAgentTextDigest 是单段文本的稳定身份（sha256，十六进制）。
func cloudAgentTextDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// cloudAgentAwaitingFirstStep 判断运行是否停在"等首个模型步"的阶段。
func cloudAgentAwaitingFirstStep(state *cloudAgentRuntime) bool {
	return state.ContractVersion >= cloudAgentContractVersionFirstStep &&
		state.Phase == cloudAgentPhaseAwaitingFirstStep
}

// cloudAgentFirstStepWaitError 校验"等首步"阶段的空任务历史是否自洽。
//
// 这是唯一允许 TaskIDs 为空的阶段，因此它必须比别的阶段更严：除了阶段标记本身，
// 还要求快照与占位任务都在 —— 运行可见、报价预留不见了（或反过来）都是坏状态。
func cloudAgentFirstStepWaitError(state *cloudAgentRuntime) error {
	if !cloudAgentAwaitingFirstStep(state) {
		return errors.New("Agent runtime task history is invalid")
	}
	if state.ActiveTaskID != "" || state.MediaTaskID != "" || state.PiNoToolTaskID != "" ||
		state.Step != 0 || state.CallIndex != 0 || len(state.Calls) != 0 {
		return errors.New("Agent runtime first-step wait has advanced work")
	}
	if state.Snapshot == nil || strings.TrimSpace(state.Snapshot.ServerPolicyHash) == "" || strings.TrimSpace(state.Snapshot.ToolsDigest) == "" {
		return errors.New("Agent runtime first-step contract snapshot is missing")
	}
	if cloudAgentTextDigest(state.Canonical.SystemPrompt) != state.Snapshot.ServerPolicyHash {
		return errors.New("Agent runtime server policy does not match the first-step snapshot")
	}
	if strings.TrimSpace(state.PlaceholderTaskID) == "" {
		return errors.New("Agent runtime first-step holding task is missing")
	}
	return nil
}

// cloudAgentContractSnapshotFor 在建 run 时固化"服务端已经知道的那部分"合同。
//
// Harness 正文此刻还拿不到（Node 才是装配方，见 cloudAgentHarnessSnapshot 的注释），
// 它在首个模型步补齐。工具 schema 在建 run 时就定型：运行期间不再重读工具目录。
func cloudAgentContractSnapshotFor(systemPrompt string, tools []map[string]any) *cloudAgentContractSnapshot {
	// 名字排序后再入快照：快照是"集合身份"，不能随工具目录的书写顺序变化而漂移。
	names := cloudAgentToolNames(tools)
	sort.Strings(names)
	return &cloudAgentContractSnapshot{
		Version:           cloudAgentContractSnapshotVersion,
		ServerPolicyHash:  cloudAgentTextDigest(systemPrompt),
		ToolSchemaVersion: strconv.Itoa(cloudAgentToolDisclosureVersion),
		ToolNames:         names,
		ToolsDigest:       cloudAgentToolsDigest(tools),
	}
}

// cloudAgentFrozenHarness 返回快照里已冻结的 Harness 正文（未冻结、无快照时为 nil）。
//
// 存在的意义只是那个 nil 判断：读快照的调用方（快照组装、比对）都必须对"迁移前的运行
// 没有快照"保持安全，把 `snapshot == nil` 的判断散在各处迟早会漏一处。
func cloudAgentFrozenHarness(snapshot *cloudAgentContractSnapshot) *cloudAgentHarnessSnapshot {
	if snapshot == nil {
		return nil
	}
	return snapshot.Harness
}

// cloudAgentVerifyFrozenContract 在每个模型步上复核"本轮合同没有被改写"。
//
// 它只对版本 2 之后的运行生效：旧运行的 Snapshot 为空，行为与迁移前一致。
func cloudAgentVerifyFrozenContract(state *cloudAgentRuntime) error {
	if state.ContractVersion < cloudAgentContractVersionFirstStep {
		return nil
	}
	if state.Snapshot == nil {
		return errors.New("Agent runtime first-step contract snapshot is missing")
	}
	if cloudAgentTextDigest(state.Canonical.SystemPrompt) != state.Snapshot.ServerPolicyHash {
		return errors.New("Agent runtime server policy does not match the first-step snapshot")
	}
	return nil
}

// cloudAgentToolsDigest 是工具 schema（名字 + 参数）的规范化摘要。
//
// 为什么不能只按名字放行：模型请求里的工具名来自 Node 的披露层，同名不同 schema
// 足以改变一次调用被服务端如何解读（例如把只读工具改写成可写参数）。参数按名字排序
// 后逐个摘要，所以与列表顺序无关；描述不参与摘要 —— 披露层会按上下文动态改写描述，
// 把它算进来会让合法步骤被误判成漂移。
func cloudAgentToolsDigest(tools []map[string]any) string {
	entries := make([]string, 0, len(tools))
	for _, tool := range tools {
		function, _ := tool["function"].(map[string]any)
		if function == nil {
			function = tool
		}
		name, _ := function["name"].(string)
		payload, err := json.Marshal(map[string]any{"name": name, "parameters": function["parameters"]})
		if err != nil {
			// 无法序列化的 schema 不能静默等同：用名字单独占位，摘要必然与任何合法声明不同。
			payload = []byte("invalid:" + name)
		}
		entries = append(entries, string(payload))
	}
	sort.Strings(entries)
	hash := sha256.New()
	for _, entry := range entries {
		hash.Write([]byte(strconv.Itoa(len(entry))))
		hash.Write([]byte{':'})
		hash.Write([]byte(entry))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// cloudAgentHarnessMatchesSnapshot 判断本次提交的 Harness 是否仍是本轮的同一份。
//
// 首个模型步之后快照即为合同：哈希与正文都必须一致。先比哈希（便宜、能挡住
// "换了文件但结构相同"），哈希不一致时不再比对正文，直接给出可诊断的错误。
func cloudAgentHarnessMatchesSnapshot(snapshot *cloudAgentContractSnapshot, hash string, parts *cloudAgentHarnessSnapshot) error {
	if snapshot == nil {
		return errors.New("Agent runtime first-step contract snapshot is missing")
	}
	if strings.TrimSpace(snapshot.HarnessHash) == "" {
		return nil
	}
	frozen := strings.TrimSpace(snapshot.HarnessHash)
	if strings.TrimSpace(hash) == "" {
		return errors.New("Agent runtime prompt contract is missing")
	}
	if hash != frozen {
		return errors.New("Agent runtime prompt contract changed")
	}
	if parts != nil && !cloudAgentHarnessEqual(snapshot.Harness, parts) {
		return errors.New("Agent runtime prompt body changed")
	}
	return nil
}

// cloudAgentHarnessEqual 比较两份 Harness 正文。
//
// 它只在"冻结值与提交值"之间比较，且比较的就是 JSON 里的三个字段，因此不需要
// 哈希之外的额外规则；nil 与空段都按"没有这一段"处理。
func cloudAgentHarnessEqual(left, right *cloudAgentHarnessSnapshot) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if !cloudAgentOptionalTextEqual(left.System, right.System) || !cloudAgentOptionalTextEqual(left.AppendSystem, right.AppendSystem) {
		return false
	}
	if len(left.Context) != len(right.Context) {
		return false
	}
	for index := range left.Context {
		if left.Context[index].Name != right.Context[index].Name || left.Context[index].Text != right.Context[index].Text {
			return false
		}
	}
	return true
}

func cloudAgentOptionalTextEqual(left, right *string) bool {
	leftText, rightText := "", ""
	if left != nil {
		leftText = *left
	}
	if right != nil {
		rightText = *right
	}
	return leftText == rightText
}

// cloudAgentHoldingTask 读取并校验本轮占位任务，返回它可以被"换单/退还"的前提事实。
//
// 所有权必须逐项核对：任务属于同一用户、ID 就是运行 ID、操作名是占位操作名、
// 状态仍是 holding。任何一项不符都说明运行状态被外部改过（或有人手工改库），
// 这时宁可报错也不再动账 —— 错误的退款比不退款更难收拾。
func (s *Service) cloudAgentHoldingTask(userID, runID, placeholderTaskID string) (*model.Task, error) {
	return cloudAgentHoldingTaskFrom(s.repo, userID, runID, placeholderTaskID)
}

// cloudAgentHoldingTaskFrom 是同一校验的事务内版本。
//
// 首步换单必须在**换单那个事务里**读占位行：用事务外的副本判断"占位订单还没退"，
// 中间被别人改过账时就会按过期事实退款。这里读的是同一事务里可见的那一行。
func cloudAgentHoldingTaskFrom(repo *repository.Repository, userID, runID, placeholderTaskID string) (*model.Task, error) {
	if strings.TrimSpace(placeholderTaskID) == "" {
		return nil, errors.New("Agent runtime first-step holding task is missing")
	}
	placeholder, err := repo.TaskForUser(userID, placeholderTaskID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("Agent runtime first-step holding task is missing")
		}
		return nil, err
	}
	if placeholder.ID != runID || placeholder.Operation != cloudAgentHoldingOperation {
		return nil, errors.New("Agent runtime first-step holding task does not belong to this run")
	}
	return placeholder, nil
}

// cloudAgentFirstStepAdmissionMessage 把首步准入的仓储错误翻成用户能看懂的原因。
//
// 首步与普通任务走同一套准入，所以这里的措辞与 `CreateTask` 的对应分支保持一致：
// 同一种拒绝在两个入口出现两种说法，用户会以为遇到了两个不同的问题。
func cloudAgentFirstStepAdmissionMessage(err error) string {
	switch {
	case errors.Is(err, repository.ErrActiveTaskLimit):
		return "同时排队或运行的任务太多，请等待已有任务完成"
	case errors.Is(err, repository.ErrInsufficientCredits):
		return "积分不足，请先使用兑换码充值"
	case errors.Is(err, repository.ErrLogicalModelUnavailable):
		return "所选模型已停用、归档或配置已更新，请重新选择"
	}
	return cloudAgentSafeToolError(err)
}

// failCloudAgentFirstStepAdmission 在首步准入失败时把运行写成确定终态，并退还占位预留。
//
// 为什么必须在这里退款：运行停在 awaiting_first_step 时，占位预留是这一轮唯一"冻结中的钱"。
// 只把状态改成 failed，退款就要等看门狗清扫（未领取 30 分钟），而这期间用户已经看到失败，
// 账上却仍冻结着 —— 终态与退款必须在同一事务里成立。
//
// CleanupPending 仍置 true 并把预留在同一事务里退掉：收尾路径还会取消子任务、释放资源租约，
// 而它对同一订单的第二次退款是幂等的（refundBillingOrder 见到已退款直接返回）。
func (s *Service) failCloudAgentFirstStepAdmission(run *model.CloudAgentExecution, state *cloudAgentRuntime, cause error) error {
	message := cloudAgentFirstStepAdmissionMessage(cause)
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		placeholder, err := cloudAgentHoldingTaskFrom(repo, run.UserID, run.ID, state.PlaceholderTaskID)
		if err != nil {
			return err
		}
		if err := repo.RefundCloudAgentHoldingOrder(placeholder, "Agent 首步准入失败，占位预留退还"); err != nil {
			return err
		}
		current.Status = "failed"
		current.FailureMessage = truncateRunes(message, 1000)
		current.CleanupPending = true
		state.event(run.ID, "run_failed", map[string]any{"text": message, "reason": "first_step_admission_failed"})
		return cloudAgentSave(current, state)
	})
}

// cloudAgentRefundHoldingReservation 在**调用方事务里**退还本轮占位预留。
//
// 判据刻意不看解码后的状态、也不看阶段字符串，只看占位行本身：
//   - 换单成功时占位订单已经退过，再退一次是 no-op（refundBillingOrder 幂等）；
//   - 换单从未发生时，这是唯一会来退它的地方；
//   - 迁移前的运行没有占位行，直接跳过。
//
// 为什么不依赖状态：收尾路径可能拿不到可解码的 StateJSON（检查点行不完整就会解码失败）。
// 把"解码失败"变成"不用退钱"，等于把那笔预留永久冻在账上 —— 而占位行是同一笔事务写下的
// 事实，它比状态更可靠。
func cloudAgentRefundHoldingReservation(repo *repository.Repository, run *model.CloudAgentExecution, note string) error {
	placeholder, err := repo.HoldingTaskForRun(run.UserID, run.ID, cloudAgentHoldingOperation)
	if err != nil {
		return err
	}
	if placeholder == nil {
		return nil
	}
	return repo.RefundCloudAgentHoldingOrder(placeholder, note)
}

// cloudAgentHarnessBodyDigest 复算 Node `harnessHash` 的内容身份。
//
// 为什么要在 Go 侧复算而不是直接信 Node 报的哈希：Node 同时提交"哈希"和"正文"，
// 只比对哈希等于自证；写进快照的正文与它声称的身份必须是同一份事实。算法与
// agent/src/system-prompt.ts 的 harnessHash 一一对应 —— 每段先写 `标签:字节长度:`，
// 再写正文，标签固定顺序（system → context:<name> → appendSystem）。
//
// 长度前缀不能用分隔符代替：文件正文里可能出现任何分隔符，只有长度前缀是无歧义的。
func cloudAgentHarnessBodyDigest(parts *cloudAgentHarnessSnapshot) string {
	system, appendSystem := "", ""
	var context []cloudAgentHarnessPart
	if parts != nil {
		if parts.System != nil {
			system = *parts.System
		}
		if parts.AppendSystem != nil {
			appendSystem = *parts.AppendSystem
		}
		context = parts.Context
	}
	hash := sha256.New()
	feed := func(label, text string) {
		hash.Write([]byte(label + ":" + strconv.Itoa(len(text)) + ":"))
		hash.Write([]byte(text))
	}
	feed("system", system)
	for _, entry := range context {
		feed("context:"+entry.Name, entry.Text)
	}
	feed("appendSystem", appendSystem)
	return hex.EncodeToString(hash.Sum(nil))
}

// cloudAgentFreezeFirstStepContract 在首个模型步把 Harness 正文与哈希固化进快照。
//
// 它只写内存里的运行态：真正的提交发生在首步换单那个事务里（`enqueueCloudAgentTask`），
// 因此"冻结合同"与"创建首步任务、退占位预留、推进阶段"是同一次受控状态转换 ——
// 事务失败时这里改过的字段全部丢弃，运行仍停在 awaiting_first_step。
//
// 阶段（Phase）刻意不在这里清：只有首步任务确实落库之后才允许离开该阶段，
// 否则崩在换单前会留下"运行以为首步已发、其实没有任何任务"的空洞。
func cloudAgentFreezeFirstStepContract(state *cloudAgentRuntime, harnessHash string, parts *cloudAgentHarnessSnapshot, assembledPrompt string) error {
	if err := cloudAgentVerifyFrozenContract(state); err != nil {
		return err
	}
	if state.Snapshot == nil {
		return errors.New("Agent runtime first-step contract snapshot is missing")
	}
	// 幂等重投：合同已冻结时只接受完全一致的重放（哈希、正文、装配结果都比）。
	if strings.TrimSpace(state.Snapshot.HarnessHash) != "" {
		if err := cloudAgentHarnessMatchesSnapshot(state.Snapshot, harnessHash, parts); err != nil {
			return err
		}
		return cloudAgentVerifyAssembledPrompt(state, assembledPrompt)
	}
	hash := strings.TrimSpace(harnessHash)
	if hash == "" {
		return errors.New("Agent runtime prompt contract is missing")
	}
	if parts == nil {
		// 只存哈希不足以恢复：重启后 Node 需要原正文，重读磁盘会静默换掉在途提示。
		return errors.New("Agent runtime first-step prompt body is missing")
	}
	if digest := cloudAgentHarnessBodyDigest(parts); digest != hash {
		return errors.New("Agent runtime prompt body does not match its hash")
	}
	state.Snapshot.HarnessHash = hash
	state.Snapshot.Harness = parts
	state.Snapshot.AssembledPromptHash = cloudAgentTextDigest(cloudAgentAssembledPromptIdentity(state, assembledPrompt))
	// 兼容旧读路径：PromptContract 是迁移前记录提示身份的字段，仍按同一值维护。
	state.PromptContract = hash
	return nil
}

// cloudAgentVerifyAssembledPrompt 比对后续步骤提交的系统提示仍是首步那一份。
//
// 比较前去掉首尾空白：装配层对同一份输入做空白规范化不算换提示，静默改写正文才算。
func cloudAgentVerifyAssembledPrompt(state *cloudAgentRuntime, assembledPrompt string) error {
	if state.Snapshot == nil || strings.TrimSpace(state.Snapshot.AssembledPromptHash) == "" {
		return errors.New("Agent runtime first-step contract snapshot is missing")
	}
	if cloudAgentTextDigest(cloudAgentAssembledPromptIdentity(state, assembledPrompt)) != state.Snapshot.AssembledPromptHash {
		return errors.New("Agent runtime system prompt changed after the first model step")
	}
	return nil
}

// cloudAgentToolSchemaDrift 校验 Node 请求里的工具参数与 Go 权威定义逐字段一致。
//
// 为什么不能只看名字：模型请求里的工具名来自 Node 的披露层，同名不同 schema 足以改变
// 一次调用被服务端如何解读（例如把只读工具改写成可写参数）。名字是否已披露由调用方
// 另行校验；这里只回答"同名工具的参数结构是不是同一份"。
//
// 比对用规范化 JSON：Go 的两个 map 由同一份目录编译而来，序列化键序稳定，
// 因此不需要额外的字段级比较规则。未在 Go 权威集合里出现的名字直接跳过。
func cloudAgentToolSchemaDrift(canonicalTools, requestTools []map[string]any) error {
	authority := make(map[string]string, len(canonicalTools))
	for _, tool := range canonicalTools {
		name, payload := cloudAgentToolSchemaIdentity(tool)
		if name != "" {
			authority[name] = payload
		}
	}
	for _, tool := range requestTools {
		name, payload := cloudAgentToolSchemaIdentity(tool)
		if name == "" {
			continue
		}
		expected, ok := authority[name]
		if !ok {
			continue
		}
		if expected != payload {
			return fmt.Errorf("Agent 工具 %s 的参数结构与 Go 权威定义不一致，请重新发起对话", name)
		}
	}
	return nil
}

// cloudAgentToolSchemaIdentity 返回单个工具名与其参数结构的规范化身份。
func cloudAgentToolSchemaIdentity(tool map[string]any) (string, string) {
	function, _ := tool["function"].(map[string]any)
	if function == nil {
		function = tool
	}
	name, _ := function["name"].(string)
	if strings.TrimSpace(name) == "" {
		return "", ""
	}
	raw, err := json.Marshal(function["parameters"])
	if err != nil {
		// 无法序列化的 schema 不能静默等同：身份必然与任何合法声明不同。
		return name, "invalid"
	}
	return name, string(raw)
}

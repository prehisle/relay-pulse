package onboarding

// 本文件是自助收录「选厂商 → 选该厂商的模型」里**第一方厂商**那一半的种子目录。
//
// 另一半（Anthropic / OpenAI / Google 的模型）不在这里：那些模型各自有专属探针模板，模板
// 里已经声明了 model / request_model / model_vendor，目录条目由 API 层从模板自动派生
// （见 internal/api/onboarding_catalog.go）。手写只覆盖 native 族——那族模板厂商无关、
// 刻意不声明模型，没有可派生的信息。
//
// ⚠️ 这些模型 ID 是**建议值不是断言**：同一个模型在厂商自家开放平台、云平台转售、聚合路由
// 上的 ID 常常不同（种子值取自本站在跑的火山方舟通道）。因此前端把选中的 ID 预填进一个
// **可编辑**输入框，用户改得动；填错的代价是探测报错重来一次，而不是卡死。
// 同理，条目里的模板只是**默认**请求形态，前端仍会让用户在该 service 的 native 变体间切换。

// FirstPartyModel 是第一方厂商的一个种子模型条目。
//
// Label 里**不要**重复厂商名：下拉按厂商分组，组标题已经写着「智谱」，选项再写一遍
// 「GLM-5.2（智谱）」是噪音。
type FirstPartyModel struct {
	// Vendor 受控词表里的厂商 code（internal/modelvendor）。
	Vendor string
	// Label 给用户看的模型名。
	Label string
	// Model 建议的模型 ID（前端预填、可改）。
	Model string
	// Template 默认的 native 探针模板：cc 侧按该模型的**思考行为**选，cx 侧按端点形态选。
	Template string
}

// firstPartyModels 按 service 分组的种子目录。
//
// cc 侧的模板选择判据（全部由真端点实测得出，别凭猜改）：默认开思考且认 thinking 开关的用
// nothink 版（最快最省）；默认开思考却不认这个开关的只能放大预算（实测 kimi 对
// thinking.disabled 返 400）；默认不开思考的用基础版。
// cx 侧两个变体的差别是**端点形态**（base_url 是否已含版本段）而非模型属性，故一律先给
// 基础版，由用户在表单里改。
var firstPartyModels = map[string][]FirstPartyModel{
	"cc": {
		{Vendor: "zhipu", Label: "GLM-5.2", Model: "glm-5.2", Template: "cc-native-arith-nothink"},
		{Vendor: "moonshot", Label: "Kimi K2.7 Code", Model: "kimi-k2.7-code", Template: "cc-native-arith-512"},
		{Vendor: "minimax", Label: "MiniMax M3", Model: "minimax-m3", Template: "cc-native-arith-nothink"},
		{Vendor: "deepseek", Label: "DeepSeek V4 Pro", Model: "deepseek-v4-pro", Template: "cc-native-arith-nothink"},
		{Vendor: "bytedance", Label: "豆包 Seed 2.1 Turbo", Model: "doubao-seed-2.1-turbo", Template: "cc-native-arith-nothink"},
	},
	"cx": {
		{Vendor: "zhipu", Label: "GLM-5.2", Model: "glm-5.2", Template: "cx-native-arith"},
		{Vendor: "moonshot", Label: "Kimi K2.7 Code", Model: "kimi-k2.7-code", Template: "cx-native-arith"},
		{Vendor: "minimax", Label: "MiniMax M3", Model: "minimax-m3", Template: "cx-native-arith"},
		{Vendor: "deepseek", Label: "DeepSeek V4 Pro", Model: "deepseek-v4-pro", Template: "cx-native-arith"},
		{Vendor: "bytedance", Label: "豆包 Seed 2.1 Turbo", Model: "doubao-seed-2.1-turbo", Template: "cx-native-arith"},
	},
}

// FirstPartyModels 返回某条 service 的种子目录副本（外部改不到真相源）。
// gm 没有条目：Gemini 协议目前没有第一方厂商拿它对外提供自家模型。
func FirstPartyModels(serviceType string) []FirstPartyModel {
	return append([]FirstPartyModel(nil), firstPartyModels[serviceType]...)
}

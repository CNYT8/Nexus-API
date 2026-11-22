package advancedcustom

// 与配置层注册值保持一致；Responses 转换接线独立于其它协议的路由实现。
const (
	responsesToChat   = "openai_responses_to_openai_chat_completions"
	responsesToClaude = "openai_responses_to_anthropic_messages"
	responsesToGemini = "openai_responses_to_gemini_generate_content"
)

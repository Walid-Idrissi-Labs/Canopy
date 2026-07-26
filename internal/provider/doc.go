// Package provider talks to model APIs.
//
// Two implementations cover the field: Anthropic, and an OpenAI compatible client whose base URL
// can be pointed anywhere, which reaches Kimi, MiniMax, DeepSeek, Groq, OpenRouter and local
// runtimes such as Ollama.
//
// The differences between the two APIs, particularly the shape of tool calls, stop here. Nothing
// above this package should be able to tell which vendor answered.
//
// Filled in by A2-02 and A2-06.
package provider

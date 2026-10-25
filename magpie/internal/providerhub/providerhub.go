// Package providerhub is MolaSearch's provider platform: one registry for
// every outside service a search may use — search engines, embedding,
// rerank, vision, speech, vector stores, models and agent tools — with the
// keys each one needs, whether it is connected, and a real connectivity
// check per provider.
//
// Nothing here pretends: a provider with no key reads "not configured", a
// check is an actual call to the vendor with the saved key, and a provider
// the user turned off refuses to be checked.
package providerhub

import "sort"

// Category is one shelf of the platform.
type Category string

const (
	CatSearch    Category = "search"
	CatEmbedding Category = "embedding"
	CatRerank    Category = "rerank"
	CatVision    Category = "vision"
	CatLLM       Category = "llm"
	CatSTT       Category = "stt"
	CatTTS       Category = "tts"
	CatVector    Category = "vector"
	CatTools     Category = "tools"
)

// Categories is every shelf, in the order the page shows them.
var Categories = []Category{CatSearch, CatEmbedding, CatRerank, CatVision, CatLLM, CatSTT, CatTTS, CatVector, CatTools}

// CategoryLabel is the English source string the page translates.
func CategoryLabel(c Category) string {
	switch c {
	case CatSearch:
		return "Search"
	case CatEmbedding:
		return "Query embedding"
	case CatRerank:
		return "Rerank"
	case CatVision:
		return "Vision & image rerank"
	case CatLLM:
		return "Models"
	case CatSTT:
		return "Speech to text"
	case CatTTS:
		return "Text to speech"
	case CatVector:
		return "Vector stores"
	case CatTools:
		return "Agent tools"
	}
	return string(c)
}

// Field is one setting a provider reads from the .env file.
type Field struct {
	Env         string `json:"env"`
	Key         string `json:"key"`
	Secret      bool   `json:"secret"`
	Required    bool   `json:"required"`
	Placeholder string `json:"placeholder"`
}

// ProbeKind says how a provider's connection is checked.
type ProbeKind string

const (
	ProbeChannel      ProbeKind = "search-channel"
	ProbeOpenAI       ProbeKind = "openai-models"
	ProbeAnthropic    ProbeKind = "anthropic-models"
	ProbeGemini       ProbeKind = "gemini-models"
	ProbeJina         ProbeKind = "jina-models"
	ProbeCohere       ProbeKind = "cohere-models"
	ProbeVoyageEmbed  ProbeKind = "voyage-embed"
	ProbeVoyageRerank ProbeKind = "voyage-rerank"
	ProbeFirecrawl    ProbeKind = "firecrawl-credits"
	ProbeDeepgram     ProbeKind = "deepgram-projects"
	ProbeAssembly     ProbeKind = "assembly-account"
	ProbeEleven       ProbeKind = "eleven-user"
	ProbeQdrant       ProbeKind = "qdrant-collections"
	ProbePinecone     ProbeKind = "pinecone-indexes"
	ProbeWeaviate     ProbeKind = "weaviate-ready"
	ProbeChroma       ProbeKind = "chroma-heartbeat"
	ProbeMilvus       ProbeKind = "milvus-health"
	ProbeOllama       ProbeKind = "ollama-tags"
	ProbeMCP          ProbeKind = "mcp-initialize"
	ProbeLocalPlugin  ProbeKind = "local-plugin"
	ProbePostgres     ProbeKind = "postgres-dial"
)

// Provider is one service the platform can register.
type Provider struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Category Category `json:"category"`
	// Icon is a glyph name the page draws; Tone is a MolaMaker token.
	Icon string `json:"icon"`
	Tone string `json:"tone"`
	// Docs is the vendor's key page.
	Docs string `json:"docs"`
	// Note is a one-line vendor description for API consumers; the page
	// does not show it, so it stays in the backend's words.
	Note string `json:"note"`
	// A provider can be keyless: it works with no key at all (local or
	// public routes).
	Keyless bool `json:"keyless"`
	// Fields are the .env names this provider writes.
	Fields []Field `json:"fields"`
	// Modes are alternative complete key sets: a provider is configured when
	// any one mode has every name in it set. Empty means every required field.
	Modes [][]string `json:"-"`
	// Channel is the engine channel a search provider maps to.
	Channel string `json:"channel,omitempty"`
	// Wired says the engine itself asks this provider today; a registered
	// provider without a channel is connectable but not yet in a run.
	Wired bool `json:"wired"`
	// Slot is the engine slot (embedding, rerank, vision) whose keys this
	// provider writes; one provider per slot is active.
	Slot string `json:"slot,omitempty"`
	// Probe checks the connection.
	Probe ProbeKind `json:"-"`
	// ProbeURL is a default endpoint for probes that do not use a base field.
	ProbeURL string `json:"-"`
	// Needle finds a local plugin for ProbeLocalPlugin.
	Needle string `json:"-"`
}

func fld(env, key string, secret, required bool, placeholder string) Field {
	return Field{Env: env, Key: key, Secret: secret, Required: required, Placeholder: placeholder}
}

// slotFields is the base/key/model trio the engine reads for a slot.
func slotFields(prefix, keyKey string) []Field {
	return []Field{
		fld("MOLA_"+prefix+"_API_BASE", "API base", false, true, "https://…"),
		fld("MOLA_"+prefix+"_API_KEY", keyKey, true, true, "sk-…"),
		fld("MOLA_"+prefix+"_MODEL", "Model", false, true, "model name"),
	}
}

// slotFieldsOptionalKey is slotFields for a vendor that runs on this
// computer and usually takes no key (Ollama); the base and the model still
// make the slot complete.
func slotFieldsOptionalKey(prefix, keyKey string) []Field {
	fs := slotFields(prefix, keyKey)
	fs[1].Required = false
	return fs
}

// registry is every provider, in the order the page shows them.
func registry() []Provider {
	return []Provider{
		// ---- search -----------------------------------------------------
		{
			ID: "tencent", Name: "元宝 SearchPro", Category: CatSearch, Icon: "spark", Tone: "pink",
			Docs: "https://cloud.tencent.com/document/product/1806",
			Note: "Tencent Cloud WSA SearchPro, signed with your SecretId/SecretKey, or the vendor's MCP endpoint with a WSA API key",
			Fields: []Field{
				fld("TENCENT_SECRET_ID", "SecretId", false, false, "AKID…"),
				fld("TENCENT_SECRET_KEY", "SecretKey", true, false, ""),
				fld("TENCENT_WSA_API_KEY", "WSA API key (MCP)", true, false, ""),
			},
			Modes:   [][]string{{"TENCENT_SECRET_ID", "TENCENT_SECRET_KEY"}, {"TENCENT_WSA_API_KEY"}},
			Channel: "tencent", Probe: ProbeChannel, ProbeURL: "人工智能最新进展",
		},
		{
			ID: "exa", Name: "Exa", Category: CatSearch, Icon: "search", Tone: "violet",
			Docs:    "https://dashboard.exa.ai/api-keys",
			Note:    "Neural search with page text, for research-heavy queries",
			Fields:  []Field{fld("EXA_API_KEY", "API key", true, true, "exa-…")},
			Channel: "exa", Probe: ProbeChannel, ProbeURL: "MolaSearch connectivity check",
		},
		{
			ID: "tavily", Name: "Tavily", Category: CatSearch, Icon: "search", Tone: "sky",
			Docs:    "https://app.tavily.com/home",
			Note:    "Search API built for agents, with a short answer per query",
			Fields:  []Field{fld("TAVILY_API_KEY", "API key", true, true, "tvly-…")},
			Channel: "tavily", Probe: ProbeChannel, ProbeURL: "MolaSearch connectivity check",
		},
		{
			ID: "brave", Name: "Brave Search", Category: CatSearch, Icon: "globe", Tone: "amber",
			Docs:    "https://api-dashboard.search.brave.com/app/subscriptions",
			Note:    "Independent web index; strong on fresh news",
			Fields:  []Field{fld("BRAVE_API_KEY", "API key", true, true, "BSA…")},
			Channel: "brave", Probe: ProbeChannel, ProbeURL: "MolaSearch connectivity check",
		},
		{
			ID: "searxng", Name: "SearXNG", Category: CatSearch, Icon: "layers", Tone: "green",
			Docs: "https://docs.searxng.org/admin/installation.html",
			Note: "Your own metasearch instance, no vendor quota",
			Fields: []Field{
				fld("SEARXNG_URL", "Instance URL", false, true, "http://127.0.0.1:8080"),
				fld("SEARXNG_API_KEY", "API key (if your instance needs one)", true, false, ""),
			},
			Channel: "searxng", Probe: ProbeChannel, ProbeURL: "MolaSearch connectivity check",
		},
		{
			ID: "firecrawl", Name: "Firecrawl", Category: CatSearch, Icon: "flame", Tone: "amber",
			Docs:     "https://www.firecrawl.dev/app/api-keys",
			Note:     "Crawls and reads pages for search results that need the full text",
			Fields:   []Field{fld("FIRECRAWL_API_KEY", "API key", true, true, "fc-…")},
			Probe:    ProbeFirecrawl,
			ProbeURL: "https://api.firecrawl.dev/v1/team/credit-usage",
		},
		{
			ID: "github", Name: "GitHub", Category: CatSearch, Icon: "code", Tone: "ink",
			Docs:    "https://docs.github.com/en/rest/search",
			Note:    "Code, repositories and issues; works without a key",
			Keyless: true, Channel: "github", Probe: ProbeChannel, ProbeURL: "molasearch",
		},
		{
			ID: "wikipedia", Name: "Wikipedia", Category: CatSearch, Icon: "globe", Tone: "ink",
			Docs:    "https://www.mediawiki.org/wiki/API:Search",
			Note:    "Encyclopedic grounding for people, places and events; no key",
			Keyless: true, Channel: "wikipedia", Probe: ProbeChannel, ProbeURL: "MolaSearch",
		},
		{
			ID: "duckduckgo", Name: "DuckDuckGo", Category: CatSearch, Icon: "search", Tone: "green",
			Docs:    "https://duckduckgo.com/",
			Note:    "Keyless general web results; the last route before an honest abort",
			Keyless: true, Channel: "ddg", Probe: ProbeChannel, ProbeURL: "MolaSearch",
		},

		// ---- query embedding -------------------------------------------
		{
			ID: "openai-embed", Name: "OpenAI Embeddings", Category: CatEmbedding, Icon: "cube", Tone: "green",
			Docs:   "https://platform.openai.com/api-keys",
			Note:   "text-embedding-3 on OpenAI; writes the engine's embedding slot",
			Fields: slotFields("EMBED", "API key"),
			Slot:   "embedding", Probe: ProbeOpenAI,
		},
		{
			ID: "jina-embed", Name: "Jina Embeddings", Category: CatEmbedding, Icon: "cube", Tone: "pink",
			Docs:   "https://jina.ai/embeddings/",
			Note:   "jina-embeddings-v3, multilingual; writes the engine's embedding slot",
			Fields: slotFields("EMBED", "API key"),
			Slot:   "embedding", Probe: ProbeJina,
		},
		{
			ID: "voyage-embed", Name: "Voyage AI", Category: CatEmbedding, Icon: "cube", Tone: "blue",
			Docs:   "https://dashboard.voyageai.com/organization/api-keys",
			Note:   "voyage-3, retrieval-tuned; writes the engine's embedding slot",
			Fields: slotFields("EMBED", "API key"),
			Slot:   "embedding", Probe: ProbeVoyageEmbed,
		},
		{
			ID: "ollama-embed", Name: "Ollama (local)", Category: CatEmbedding, Icon: "cube", Tone: "ink",
			Docs:   "https://ollama.com/",
			Note:   "A model on this computer's own Ollama; no key leaves the machine",
			Fields: slotFieldsOptionalKey("EMBED", "API key (usually empty)"),
			Slot:   "embedding", Probe: ProbeOpenAI,
		},

		// ---- rerank -----------------------------------------------------
		{
			ID: "jina-rerank", Name: "Jina Reranker", Category: CatRerank, Icon: "layers", Tone: "pink",
			Docs:   "https://jina.ai/reranker/",
			Note:   "Cross-encoder rerank of the merged pool; writes the engine's rerank slot",
			Fields: slotFields("RERANK", "API key"),
			Slot:   "rerank", Probe: ProbeJina,
		},
		{
			ID: "cohere-rerank", Name: "Cohere Rerank", Category: CatRerank, Icon: "layers", Tone: "violet",
			Docs:   "https://dashboard.cohere.com/api-keys",
			Note:   "rerank-v3.5 over the top candidates; writes the engine's rerank slot",
			Fields: slotFields("RERANK", "API key"),
			Slot:   "rerank", Probe: ProbeCohere,
		},
		{
			ID: "voyage-rerank", Name: "Voyage Rerank", Category: CatRerank, Icon: "layers", Tone: "blue",
			Docs:   "https://dashboard.voyageai.com/organization/api-keys",
			Note:   "rerank-2; writes the engine's rerank slot",
			Fields: slotFields("RERANK", "API key"),
			Slot:   "rerank", Probe: ProbeVoyageRerank,
		},

		// ---- vision and image rerank -----------------------------------
		{
			ID: "openai-vision", Name: "OpenAI Vision", Category: CatVision, Icon: "eye", Tone: "green",
			Docs:   "https://platform.openai.com/api-keys",
			Note:   "Reads and ranks images a result carries; writes the engine's vision slot",
			Fields: slotFields("VISION", "API key"),
			Slot:   "vision", Probe: ProbeOpenAI,
		},
		{
			ID: "qwen-vision", Name: "Qwen VL", Category: CatVision, Icon: "eye", Tone: "violet",
			Docs:   "https://bailian.console.aliyun.com/",
			Note:   "Qwen's vision models through the OpenAI-compatible endpoint",
			Fields: slotFields("VISION", "API key"),
			Slot:   "vision", Probe: ProbeOpenAI,
		},

		// ---- models -----------------------------------------------------
		{
			ID: "deepseek", Name: "DeepSeek", Category: CatLLM, Icon: "brain", Tone: "blue",
			Docs: "https://platform.deepseek.com/api_keys",
			Note: "Answer model and debate judge; OpenAI-compatible",
			Fields: []Field{
				fld("DEEPSEEK_API_KEY", "API key", true, true, "sk-…"),
			},
			Probe: ProbeOpenAI, ProbeURL: "https://api.deepseek.com/v1",
		},
		{
			ID: "openai", Name: "OpenAI", Category: CatLLM, Icon: "spark", Tone: "green",
			Docs:     "https://platform.openai.com/api-keys",
			Note:     "Chat and reasoning models through the OpenAI API",
			Fields:   []Field{fld("OPENAI_API_KEY", "API key", true, true, "sk-…")},
			Probe:    ProbeOpenAI,
			ProbeURL: "https://api.openai.com/v1",
		},
		{
			ID: "anthropic", Name: "Anthropic", Category: CatLLM, Icon: "spark", Tone: "amber",
			Docs:     "https://console.anthropic.com/settings/keys",
			Note:     "Claude models through the Anthropic API",
			Fields:   []Field{fld("ANTHROPIC_API_KEY", "API key", true, true, "sk-ant-…")},
			Probe:    ProbeAnthropic,
			ProbeURL: "https://api.anthropic.com/v1",
		},
		{
			ID: "gemini", Name: "Google Gemini", Category: CatLLM, Icon: "spark", Tone: "sky",
			Docs:     "https://aistudio.google.com/apikey",
			Note:     "Gemini models through Google AI Studio keys",
			Fields:   []Field{fld("GEMINI_API_KEY", "API key", true, true, "AIza…")},
			Probe:    ProbeGemini,
			ProbeURL: "https://generativelanguage.googleapis.com",
		},
		{
			ID: "openrouter", Name: "OpenRouter", Category: CatLLM, Icon: "globe", Tone: "violet",
			Docs:     "https://openrouter.ai/settings/keys",
			Note:     "One key over many vendors' models",
			Fields:   []Field{fld("OPENROUTER_API_KEY", "API key", true, true, "sk-or-…")},
			Probe:    ProbeOpenAI,
			ProbeURL: "https://openrouter.ai/api/v1",
		},
		{
			ID: "ollama", Name: "Ollama (local)", Category: CatLLM, Icon: "cube", Tone: "ink",
			Docs:     "https://ollama.com/",
			Note:     "Models served on this computer; no key needed",
			Fields:   []Field{fld("OLLAMA_BASE_URL", "Base URL", false, false, "http://127.0.0.1:11434")},
			Keyless:  true,
			Probe:    ProbeOllama,
			ProbeURL: "http://127.0.0.1:11434",
		},

		// ---- speech to text --------------------------------------------
		{
			ID: "openai-whisper", Name: "Whisper (OpenAI)", Category: CatSTT, Icon: "mic", Tone: "green",
			Docs:   "https://platform.openai.com/api-keys",
			Note:   "Audio transcription through OpenAI's whisper models",
			Fields: slotFields("STT", "API key"),
			Slot:   "stt", Probe: ProbeOpenAI,
		},
		{
			ID: "groq-whisper", Name: "Groq Whisper", Category: CatSTT, Icon: "bolt", Tone: "amber",
			Docs:   "https://console.groq.com/keys",
			Note:   "Very fast Whisper through Groq's OpenAI-compatible API",
			Fields: slotFields("STT", "API key"),
			Slot:   "stt", Probe: ProbeOpenAI,
		},
		{
			ID: "deepgram", Name: "Deepgram", Category: CatSTT, Icon: "wave", Tone: "violet",
			Docs:   "https://console.deepgram.com/",
			Note:   "Live and recorded transcription; its own API shape",
			Fields: slotFields("STT", "API key"),
			Slot:   "stt", Probe: ProbeDeepgram, ProbeURL: "https://api.deepgram.com/v1",
		},
		{
			ID: "assemblyai", Name: "AssemblyAI", Category: CatSTT, Icon: "wave", Tone: "sky",
			Docs:   "https://www.assemblyai.com/app/account",
			Note:   "Recorded transcription with speaker labels",
			Fields: slotFields("STT", "API key"),
			Slot:   "stt", Probe: ProbeAssembly, ProbeURL: "https://api.assemblyai.com/v2",
		},

		// ---- text to speech -------------------------------------------
		{
			ID: "elevenlabs", Name: "ElevenLabs", Category: CatTTS, Icon: "speaker", Tone: "pink",
			Docs:   "https://elevenlabs.io/app/settings/api-keys",
			Note:   "Natural speech for read-aloud reports",
			Fields: slotFields("TTS", "API key"),
			Slot:   "tts", Probe: ProbeEleven, ProbeURL: "https://api.elevenlabs.io/v1",
		},
		{
			ID: "openai-tts", Name: "OpenAI TTS", Category: CatTTS, Icon: "speaker", Tone: "green",
			Docs:   "https://platform.openai.com/api-keys",
			Note:   "Speech synthesis through OpenAI's audio models",
			Fields: slotFields("TTS", "API key"),
			Slot:   "tts", Probe: ProbeOpenAI,
		},

		// ---- vector stores --------------------------------------------
		{
			ID: "qdrant", Name: "Qdrant", Category: CatVector, Icon: "database", Tone: "pink",
			Docs: "https://cloud.qdrant.io/",
			Note: "Vector search with payload filters",
			Fields: []Field{
				fld("MOLA_VECTOR_QDRANT_URL", "URL", false, true, "http://127.0.0.1:6333"),
				fld("MOLA_VECTOR_QDRANT_API_KEY", "API key", true, false, ""),
			},
			Probe: ProbeQdrant,
		},
		{
			ID: "pinecone", Name: "Pinecone", Category: CatVector, Icon: "database", Tone: "green",
			Docs: "https://app.pinecone.io/organizations/-/projects",
			Note: "Managed vector indexes",
			Fields: []Field{
				fld("MOLA_VECTOR_PINECONE_URL", "Index host", false, true, "https://…pinecone.io"),
				fld("MOLA_VECTOR_PINECONE_API_KEY", "API key", true, true, ""),
			},
			Probe: ProbePinecone,
		},
		{
			ID: "weaviate", Name: "Weaviate", Category: CatVector, Icon: "database", Tone: "violet",
			Docs: "https://console.weaviate.cloud/",
			Note: "Vector database with hybrid search",
			Fields: []Field{
				fld("MOLA_VECTOR_WEAVIATE_URL", "URL", false, true, "http://127.0.0.1:8080"),
				fld("MOLA_VECTOR_WEAVIATE_API_KEY", "API key", true, false, ""),
			},
			Probe: ProbeWeaviate,
		},
		{
			ID: "chroma", Name: "Chroma", Category: CatVector, Icon: "database", Tone: "amber",
			Docs: "https://docs.trychroma.com/",
			Note: "Lightweight local vector store",
			Fields: []Field{
				fld("MOLA_VECTOR_CHROMA_URL", "URL", false, true, "http://127.0.0.1:8000"),
				fld("MOLA_VECTOR_CHROMA_API_KEY", "API key", true, false, ""),
			},
			Probe: ProbeChroma,
		},
		{
			ID: "milvus", Name: "Milvus", Category: CatVector, Icon: "database", Tone: "sky",
			Docs: "https://milvus.io/docs",
			Note: "Scale-out vector database",
			Fields: []Field{
				fld("MOLA_VECTOR_MILVUS_URL", "URL", false, true, "http://127.0.0.1:9091"),
				fld("MOLA_VECTOR_MILVUS_API_KEY", "API key", true, false, ""),
			},
			Probe: ProbeMilvus,
		},
		{
			ID: "pgvector", Name: "PostgreSQL + pgvector", Category: CatVector, Icon: "database", Tone: "ink",
			Docs: "https://github.com/pgvector/pgvector",
			Note: "Vectors in the database already next to your data",
			Fields: []Field{
				fld("MOLA_PG_DSN", "DSN", true, true, "postgres://user:***@host:5432/db"),
			},
			Probe: ProbePostgres,
		},

		// ---- agent tools ----------------------------------------------
		{
			ID: "context7", Name: "Context7", Category: CatTools, Icon: "book", Tone: "blue",
			Docs: "https://context7.com/dashboard",
			Note: "Live library documentation as an MCP tool",
			Fields: []Field{
				fld("CONTEXT7_MCP_URL", "MCP endpoint", false, false, "https://mcp.context7.com/mcp"),
				fld("CONTEXT7_API_KEY", "API key", true, false, ""),
			},
			Keyless:  true,
			Probe:    ProbeMCP,
			ProbeURL: "https://mcp.context7.com/mcp",
		},
		{
			ID: "oh-my-claudecode", Name: "oh-my-claudecode", Category: CatTools, Icon: "bolt", Tone: "violet",
			Docs:    "https://github.com/oh-my-claudecode",
			Note:    "The agent plugin suite on this computer; found, not keyed",
			Keyless: true, Probe: ProbeLocalPlugin, Needle: "oh-my-claudecode",
		},
		{
			ID: "superpowers", Name: "Superpowers", Category: CatTools, Icon: "spark", Tone: "pink",
			Docs:    "https://github.com/obra/superpowers",
			Note:    "The skills plugin suite on this computer; found, not keyed",
			Keyless: true, Probe: ProbeLocalPlugin, Needle: "superpowers",
		},
	}
}

// All returns every provider sorted by category order, then name.
func All() []Provider {
	ps := registry()
	rank := map[Category]int{}
	for i, c := range Categories {
		rank[c] = i
	}
	for i := range ps {
		ps[i].Wired = ps[i].Channel != ""
	}
	sort.SliceStable(ps, func(i, j int) bool {
		if rank[ps[i].Category] != rank[ps[j].Category] {
			return rank[ps[i].Category] < rank[ps[j].Category]
		}
		return ps[i].Name < ps[j].Name
	})
	return ps
}

// ByID finds one provider.
func ByID(id string) (Provider, bool) {
	for _, p := range All() {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// RequiredEnv is every name a provider reads, in field order.
func (p Provider) RequiredEnv() []string {
	out := make([]string, 0, len(p.Fields))
	for _, f := range p.Fields {
		out = append(out, f.Env)
	}
	return out
}

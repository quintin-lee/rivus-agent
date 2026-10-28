package httpapi

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"rivus-agent-backend/internal/config"
	"rivus-agent-backend/internal/eventshub"
	"rivus-agent-backend/internal/service"
	"rivus-agent-backend/internal/store"
)

// SSE follow 循环参数；单测可覆盖为小值。
var (
	followPollInterval   = 1 * time.Second
	followHeartbeatEvery = 15 * time.Second
	followMaxDuration    = 5 * time.Minute
)

type Server struct {
	cfg      config.Config
	log      *slog.Logger
	db       *sql.DB
	tasks    *store.TaskRepo
	events   *store.EventRepo
	settings *store.SettingsRepo
	memories *store.MemoryRepo
	runs     *service.RunService
	sessions *service.SessionService
	tools    ToolConfigProvider
	hub      *eventshub.Hub
	mux      *http.ServeMux
}

// ToolConfigProvider backs the /settings/tools endpoints. The httpapi layer
// does not import internal/mcp or internal/skill — the App supplies this impl.
type ToolConfigProvider interface {
	GetToolsStatus(ctx context.Context) (ToolStatus, error)
	SaveTools(ctx context.Context, in ToolConfigIn) error
}

// ToolStatus is the GET /settings/tools payload (per-server shape mirrors
// mcp.StatusEntry, but httpapi defines its own type to stay decoupled).
type ToolStatus struct {
	Skills SkillsStatus `json:"skills"`
	MCP    MCPStatus    `json:"mcp"`
}

type SkillsStatus struct {
	Enabled   bool        `json:"enabled"`
	Available []SkillInfo `json:"available"`
}

type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type MCPStatus struct {
	Enabled bool            `json:"enabled"`
	Servers []MCPServerInfo `json:"servers"`
}

type MCPServerInfo struct {
	Name    string     `json:"name"`
	Status  string     `json:"status"` // "up" | "down" | "error"
	Command string     `json:"command"`
	Args    []string   `json:"args,omitempty"`
	Type    string     `json:"type,omitempty"`
	URL     string     `json:"url,omitempty"`
	Tools   []ToolInfo `json:"tools"`
}

type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ToolConfigIn is the PUT /settings/tools payload.
type ToolConfigIn struct {
	SkillsEnabled bool          `json:"skillsEnabled"`
	MCPEnabled    bool          `json:"mcpEnabled"`
	MCPServers    []MCPServerIn `json:"mcpServers"`
}

type MCPServerIn struct {
	Name    string            `json:"name"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Type    string            `json:"type,omitempty"` // "" | "stdio" | "http"
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Option configures an httpapi.Server. Existing 7-arg New(...) calls stay
// valid; opt in to the tools API via WithToolConfigProvider(...).
type Option func(*serverOptions)

type serverOptions struct {
	tools ToolConfigProvider
	hub   *eventshub.Hub
	log   *slog.Logger
}

func WithToolConfigProvider(p ToolConfigProvider) Option {
	return func(o *serverOptions) { o.tools = p }
}
func WithEventHub(hub *eventshub.Hub) Option {
	return func(o *serverOptions) { o.hub = hub }
}

func WithLogger(l *slog.Logger) Option {
	return func(o *serverOptions) { o.log = l }
}

func New(cfg config.Config, db *sql.DB, tasks *store.TaskRepo, events *store.EventRepo, settings *store.SettingsRepo, runs *service.RunService, sessions *service.SessionService, opts ...Option) *Server {
	o := serverOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	s := &Server{cfg: cfg, db: db, tasks: tasks, events: events, settings: settings, runs: runs, sessions: sessions, tools: o.tools, hub: o.hub, mux: http.NewServeMux()}
	s.log = o.log
	if s.log == nil {
		s.log = slog.Default()
	}
	if db != nil {
		s.memories = store.NewMemoryRepo(db)
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.securityHeaders(s.requestID(s.requestLog(s.auth(s.rateLimit(s.mux)))))
}

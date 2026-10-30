package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	httpapi "rivus-agent-backend/internal/api/http"
	"rivus-agent-backend/internal/mcp"
	"rivus-agent-backend/internal/skill"
	"rivus-agent-backend/internal/store"
	"rivus-agent-backend/internal/tool"
)

// toolConfigProvider implements httpapi.ToolConfigProvider. App owns the
// SettingsRepo, the mcp.Manager, and the skill.Loader, so it bridges them to
// the HTTP layer without the HTTP layer importing mcp/skill.
type toolConfigProvider struct {
	mu       sync.Mutex
	settings *store.SettingsRepo
	manager  *mcp.Manager
	loader   *skill.Loader
	registry *tool.Registry
	runner   runnerInstr // the guarded skill-instruction setter (§6 delta a)
}

type runnerInstr interface {
	SetSkillInstructions(listing string)
}

func newToolConfigProvider(settings *store.SettingsRepo, manager *mcp.Manager,
	loader *skill.Loader, registry *tool.Registry, runner runnerInstr) *toolConfigProvider {
	return &toolConfigProvider{settings: settings, manager: manager, loader: loader,
		registry: registry, runner: runner}
}

func (p *toolConfigProvider) GetToolsStatus(ctx context.Context) (httpapi.ToolStatus, error) {
	tc, err := mcp.ReadToolConfig(ctx, p.settings)
	if err != nil {
		return httpapi.ToolStatus{}, err
	}
	var st httpapi.ToolStatus
	st.Skills.Enabled = tc.SkillsEnabled
	for _, m := range p.loader.List() {
		st.Skills.Available = append(st.Skills.Available, httpapi.SkillInfo{Name: m.Name, Description: m.Description})
	}
	st.MCP.Enabled = tc.MCPEnabled
	for _, e := range p.manager.Status() {
		infos := make([]httpapi.ToolInfo, 0, len(e.Tools))
		for _, tl := range e.Tools {
			infos = append(infos, httpapi.ToolInfo{Name: tl.Name, Description: tl.Description})
		}
		st.MCP.Servers = append(st.MCP.Servers, httpapi.MCPServerInfo{
			Name: e.Name, Status: e.Status, Command: e.Command, Args: e.Args,
			Type: e.Type, URL: e.URL, OAuth: oauthInfo(e.OAuth), Tools: infos,
		})
	}
	return st, nil
}

// SaveTools persists a tools-config change and reconciles the live MCP
// connections + the shared registry's mcp__ family + the invoke_skill tool.
// Invariant (spec §4.1 + §6 step 4): an in-flight EinoRunner.drain keeps the
// agent snapshot it built at start; this reconcile only affects NEW agent
// builds. No extra lock is needed — registry mutations are Registry.mu-guarded
// and SetSkillInstructions is EinoRunner.mu-guarded (both from earlier tasks).
func (p *toolConfigProvider) SaveTools(ctx context.Context, in httpapi.ToolConfigIn) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 1) Persist the three settings keys.
	if err := p.settings.Set(ctx, mcp.KeySkillsEnabled, boolStr(in.SkillsEnabled)); err != nil {
		return err
	}
	if err := p.settings.Set(ctx, mcp.KeyMCPEnabled, boolStr(in.MCPEnabled)); err != nil {
		return err
	}
	servers, err := mcpServersFromIn(in)
	if err != nil {
		return err
	}
	if err := p.settings.Set(ctx, mcp.KeyMCP_servers, marshalJSON(servers)); err != nil {
		return err
	}

	// 2) Reconcile MCP connections, then refresh the shared registry's mcp__
	//    family (in-place; the runner holds the *Registry pointer). Spec §4.3:
	//    when mcp.enabled is off, reconcile to no servers so all mcp__ tools
	//    are stopped + removed from the registry.
	liveServers := servers
	if !in.MCPEnabled {
		liveServers = nil
	}
	if err := p.manager.Reconcile(ctx, liveServers); err != nil {
		return err
	}
	if err := p.registry.Replace("mcp__", mcp.MCPDefinitions(p.manager.Status(), p.manager)); err != nil {
		return err
	}

	// 3) Toggle invoke_skill + refresh the skill instruction listing.
	if in.SkillsEnabled {
		_ = p.registry.Remove("invoke_skill")
		if err := p.registry.Register(skill.InvokeSkill(p.loader, 32*1024)); err != nil {
			return err
		}
		p.runner.SetSkillInstructions(skillListing(p.loader))
	} else {
		_ = p.registry.Remove("invoke_skill")
		p.runner.SetSkillInstructions("")
	}
	return nil
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// oauthInfo 脱敏映射：client_secret 不回显，用 has_secret 代替。
func oauthInfo(o *mcp.OAuthConfig) *httpapi.MCPOAuthInfo {
	if o == nil {
		return nil
	}
	return &httpapi.MCPOAuthInfo{TokenURL: o.TokenURL, ClientID: o.ClientID,
		Scopes: o.Scopes, HasSecret: o.ClientSecret != ""}
}

func mcpServersFromIn(in httpapi.ToolConfigIn) ([]mcp.ServerSpec, error) {
	out := make([]mcp.ServerSpec, 0, len(in.MCPServers))
	for _, s := range in.MCPServers {
		var oauth *mcp.OAuthConfig
		if s.OAuth != nil {
			oauth = &mcp.OAuthConfig{TokenURL: s.OAuth.TokenURL, ClientID: s.OAuth.ClientID,
				ClientSecret: s.OAuth.ClientSecret, Scopes: s.OAuth.Scopes}
		}
		out = append(out, mcp.ServerSpec{Name: s.Name, Command: s.Command, Args: s.Args,
			Type: s.Type, URL: s.URL, Headers: s.Headers, OAuth: oauth})
	}
	if err := mcp.ValidateSpecs(out); err != nil {
		return nil, err
	}
	return out, nil
}

func marshalJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func skillListing(l *skill.Loader) string {
	out := ""
	for _, m := range l.List() {
		out += fmt.Sprintf("- %s: %s\n", m.Name, m.Description)
	}
	return out
}

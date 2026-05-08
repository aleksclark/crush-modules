package a2a

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	a2acore "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/charmbracelet/crush/plugin"
)

const (
	CleanupInterval = 5 * time.Minute
	ArtifactTTL     = 1 * time.Hour
)

func init() {
	plugin.RegisterHookWithConfig(HookName, func(ctx context.Context, app *plugin.App) (plugin.Hook, error) {
		var cfg A2AServerConfig
		if err := app.LoadConfig(HookName, &cfg); err != nil {
			return nil, err
		}
		cfg.applyEnv()
		return NewServerHook(app, cfg)
	}, &A2AServerConfig{})
}

func (c *A2AServerConfig) applyEnv() {
	if v := os.Getenv("CRUSH_A2A_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			c.Port = p
		}
	}
	if v := os.Getenv("CRUSH_A2A_AGENT_NAME"); v != "" {
		c.AgentName = v
	}
	if v := os.Getenv("CRUSH_A2A_DESCRIPTION"); v != "" {
		c.Description = v
	}
}

// ServerHook implements plugin.Hook to run an A2A v1.0 server alongside Crush.
type ServerHook struct {
	app    *plugin.App
	cfg    A2AServerConfig
	logger *slog.Logger

	artifacts *artifactStore
	card      *a2acore.AgentCard
	server    *http.Server
	addr      string
	ready     chan struct{}

	taskCtxMu   sync.RWMutex
	taskCtxMap  map[a2acore.TaskID]context.CancelFunc
	currentTask a2acore.TaskID
}

// NewServerHook creates a new A2A server hook.
func NewServerHook(app *plugin.App, cfg A2AServerConfig) (*ServerHook, error) {
	if cfg.Port <= 0 {
		cfg.Port = DefaultPort
	}
	if cfg.AgentName == "" {
		cfg.AgentName = DefaultAgentName
	}
	if cfg.Description == "" {
		cfg.Description = "Crush AI coding assistant exposed as an A2A agent"
	}

	logger := app.Logger().With("hook", HookName)

	skills := buildSkills(cfg)

	card := &a2acore.AgentCard{
		Name:        cfg.AgentName,
		Description: cfg.Description,
		Version:     "1.0.0",
		SupportedInterfaces: []*a2acore.AgentInterface{
			a2acore.NewAgentInterface(
				fmt.Sprintf("http://127.0.0.1:%d/", cfg.Port),
				a2acore.TransportProtocolJSONRPC,
			),
		},
		Capabilities: a2acore.AgentCapabilities{
			Streaming: true,
		},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills:             skills,
	}

	return &ServerHook{
		app:        app,
		cfg:        cfg,
		logger:     logger,
		artifacts:  newArtifactStore(),
		card:       card,
		ready:      make(chan struct{}),
		taskCtxMap: make(map[a2acore.TaskID]context.CancelFunc),
	}, nil
}

func buildSkills(cfg A2AServerConfig) []a2acore.AgentSkill {
	if len(cfg.Skills) > 0 {
		skills := make([]a2acore.AgentSkill, len(cfg.Skills))
		for i, s := range cfg.Skills {
			skills[i] = a2acore.AgentSkill{
				ID:          s.ID,
				Name:        s.Name,
				Description: s.Description,
				Tags:        s.Tags,
				Examples:    s.Examples,
			}
		}
		return skills
	}

	return []a2acore.AgentSkill{
		{
			ID:          "code",
			Name:        "Code Assistant",
			Description: "Write, review, debug, and refactor code across languages",
			Tags:        []string{"coding", "development"},
			Examples:    []string{"Write a function that...", "Review this code for bugs"},
		},
		{
			ID:          "tools",
			Name:        "Tool Execution",
			Description: "Execute tools like file editing, search, shell commands, and web fetching",
			Tags:        []string{"tools", "automation"},
		},
	}
}

func (h *ServerHook) Name() string { return HookName }

func (h *ServerHook) Addr() string { return h.addr }

func (h *ServerHook) Ready() <-chan struct{} { return h.ready }

// ArtifactStore returns the artifact store for use by the attach_file tool.
func (h *ServerHook) ArtifactStore() *artifactStore { return h.artifacts }

// CurrentTaskID returns the task ID of the currently executing task.
func (h *ServerHook) CurrentTaskID() a2acore.TaskID {
	h.taskCtxMu.RLock()
	defer h.taskCtxMu.RUnlock()
	return h.currentTask
}

func (h *ServerHook) Start(ctx context.Context) error {
	executor := &crushExecutor{hook: h}
	taskStore := newRetentionStore(TaskRetentionTTL, h.logger)
	requestHandler := a2asrv.NewHandler(executor,
		a2asrv.WithCapabilityChecks(&h.card.Capabilities),
		a2asrv.WithTaskStore(taskStore),
	)

	mux := http.NewServeMux()
	mux.Handle("/", a2asrv.NewJSONRPCHandler(requestHandler))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(h.card))

	addr := fmt.Sprintf(":%d", h.cfg.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	h.addr = listener.Addr().String()
	h.server = &http.Server{
		Addr:         h.addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		ticker := time.NewTicker(CleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.artifacts.Cleanup(ArtifactTTL)
			}
		}
	}()

	close(h.ready)
	h.logger.Info("A2A server started", "addr", h.addr, "agent", h.cfg.AgentName)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.server.Shutdown(shutdownCtx)
	}()

	if err := h.server.Serve(listener); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (h *ServerHook) Stop() error {
	if h.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return h.server.Shutdown(ctx)
	}
	return nil
}

// crushExecutor implements a2asrv.AgentExecutor, bridging A2A requests to the Crush prompt loop.
type crushExecutor struct {
	hook *ServerHook
}

func (e *crushExecutor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2acore.Event, error] {
	return func(yield func(a2acore.Event, error) bool) {
		text := ExtractText(execCtx.Message)
		if text == "" {
			event := a2acore.NewStatusUpdateEvent(execCtx, a2acore.TaskStateFailed,
				a2acore.NewMessage(a2acore.MessageRoleAgent, a2acore.NewTextPart("empty message")))
			yield(event, nil)
			return
		}

		submitter := e.hook.app.PromptSubmitter()
		if submitter == nil {
			event := a2acore.NewStatusUpdateEvent(execCtx, a2acore.TaskStateFailed,
				a2acore.NewMessage(a2acore.MessageRoleAgent, a2acore.NewTextPart("prompt submitter not available")))
			yield(event, nil)
			return
		}

		if execCtx.StoredTask == nil {
			task := a2acore.NewSubmittedTask(execCtx, execCtx.Message)
			if !yield(task, nil) {
				return
			}
		}

		if !yield(a2acore.NewStatusUpdateEvent(execCtx, a2acore.TaskStateWorking, nil), nil) {
			return
		}

		taskID := execCtx.TaskID
		taskCtx, cancel := context.WithCancel(ctx)
		e.hook.taskCtxMu.Lock()
		e.hook.taskCtxMap[taskID] = cancel
		e.hook.currentTask = taskID
		e.hook.taskCtxMu.Unlock()

		defer func() {
			e.hook.taskCtxMu.Lock()
			delete(e.hook.taskCtxMap, taskID)
			if e.hook.currentTask == taskID {
				e.hook.currentTask = ""
			}
			e.hook.taskCtxMu.Unlock()
			cancel()
		}()

		messages := e.hook.app.Messages()
		var eventCh <-chan plugin.MessageEvent
		var cancelWatch context.CancelFunc

		if messages != nil {
			watchCtx, wCancel := context.WithCancel(taskCtx)
			cancelWatch = wCancel
			eventCh = messages.SubscribeMessages(watchCtx)
		}

		var outputMu sync.Mutex
		var lastContent string
		var artifactID a2acore.ArtifactID
		var wg sync.WaitGroup
		events := make(chan a2acore.Event, 64)

		if eventCh != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for event := range eventCh {
					if event.Message.Role != plugin.MessageRoleAssistant {
						continue
					}
					content := event.Message.Content
					outputMu.Lock()
					if content != lastContent && content != "" {
						newContent := content
						if strings.HasPrefix(content, lastContent) {
							newContent = content[len(lastContent):]
						}
						if newContent != "" {
							var evt *a2acore.TaskArtifactUpdateEvent
							if artifactID == "" {
								evt = a2acore.NewArtifactEvent(execCtx, a2acore.NewTextPart(newContent))
								artifactID = evt.Artifact.ID
							} else {
								evt = a2acore.NewArtifactUpdateEvent(execCtx, artifactID, a2acore.NewTextPart(newContent))
							}
							select {
							case events <- evt:
							default:
							}
						}
						lastContent = content
					}
					outputMu.Unlock()
				}
			}()
		}

		go func() {
			defer close(events)

			contextID := execCtx.ContextID
			var promptErr error
			if contextID != "" {
				promptErr = submitter.SubmitPromptToSession(taskCtx, contextID, text)
			} else {
				promptErr = submitter.SubmitPrompt(taskCtx, text)
			}

			if cancelWatch != nil {
				cancelWatch()
			}
			wg.Wait()

			if promptErr != nil {
				events <- a2acore.NewStatusUpdateEvent(execCtx, a2acore.TaskStateFailed,
					a2acore.NewMessage(a2acore.MessageRoleAgent, a2acore.NewTextPart(promptErr.Error())))
				return
			}

			stagedArtifacts := e.hook.artifacts.Take(taskID)
			for _, art := range stagedArtifacts {
				events <- a2acore.NewArtifactEvent(execCtx, art.Parts...)
			}

			events <- a2acore.NewStatusUpdateEvent(execCtx, a2acore.TaskStateCompleted, nil)
		}()

		for evt := range events {
			if !yield(evt, nil) {
				return
			}
		}
	}
}

func (e *crushExecutor) Cancel(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2acore.Event, error] {
	return func(yield func(a2acore.Event, error) bool) {
		taskID := execCtx.TaskID

		e.hook.taskCtxMu.RLock()
		cancelFn, ok := e.hook.taskCtxMap[taskID]
		e.hook.taskCtxMu.RUnlock()

		if ok {
			cancelFn()
		}

		yield(a2acore.NewStatusUpdateEvent(execCtx, a2acore.TaskStateCanceled, nil), nil)
	}
}

package mcpserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/dotcontrol/dotcontrol/internal/dotbridge"
	"github.com/dotcontrol/dotcontrol/internal/dotevents"
	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
	"github.com/dotcontrol/dotcontrol/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type TaskClaimArgs struct {
	QueueID  string `json:"queue_id"`
	JobID    string `json:"job_id"`
	WorkerID string `json:"worker_id"`
}

type TaskProgressArgs struct {
	JobID   string `json:"job_id"`
	Token   string `json:"token"`
	State   string `json:"state"`
	Summary string `json:"summary"`
}

type TaskCompleteArgs struct {
	JobID  string            `json:"job_id"`
	Token  string            `json:"token"`
	Result dotqueue.Result   `json:"result"`
}

type TaskStatusArgs struct {
	JobID string `json:"job_id"`
	Token string `json:"token"`
}

type EventsListParams struct {
	mcp.ParamsBase
}

type EventsListResult struct {
	mcp.ResultBase
	Events []dotevents.Definition `json:"events"`
}

type EventsSubscribeParams struct {
	mcp.ParamsBase
	Params dotevents.Params `json:"params"`
}

type EventsSubscribeResult struct {
	mcp.ResultBase
	Subscription dotevents.Subscription `json:"subscription"`
}

type EventsUnsubscribeParams struct {
	mcp.ParamsBase
	Params dotevents.Params `json:"params"`
}

type EventsUnsubscribeResult struct {
	mcp.ResultBase
}

// NewWithBridge keeps the default 13-tool catalog and opt-in task/event methods.
func NewWithBridge(backend service.Backend, bridge *dotbridge.Service) (*mcp.Server, error) {
	s := New(backend)
	write := &mcp.ToolAnnotations{ReadOnlyHint: false}
	read := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	mcp.AddTool(s, &mcp.Tool{Name: "dot_task_claim", Description: "Claim one queued job for the authorized Dot queue. queue_id must match the configured bridge queue.", Annotations: write}, func(ctx context.Context, _ *mcp.CallToolRequest, a TaskClaimArgs) (*mcp.CallToolResult, any, error) {
		claim, err := bridge.ClaimTask(a.QueueID, a.JobID, a.WorkerID)
		if err != nil {
			return toolErr(err), nil, nil
		}
		return nil, claim, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "dot_task_progress", Description: "Report progress for a claimed job using its claim token.", Annotations: write}, func(ctx context.Context, _ *mcp.CallToolRequest, a TaskProgressArgs) (*mcp.CallToolResult, any, error) {
		job, err := bridge.ProgressTask(a.JobID, a.Token, a.State, a.Summary)
		if err != nil {
			return toolErr(err), nil, nil
		}
		return nil, job, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "dot_task_complete", Description: "Complete a claimed job using its claim token.", Annotations: write}, func(ctx context.Context, _ *mcp.CallToolRequest, a TaskCompleteArgs) (*mcp.CallToolResult, any, error) {
		job, err := bridge.CompleteTask(a.JobID, a.Token, a.Result)
		if err != nil {
			return toolErr(err), nil, nil
		}
		return nil, job, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "dot_task_status", Description: "Read a claimed job using its claim token.", Annotations: read}, func(ctx context.Context, _ *mcp.CallToolRequest, a TaskStatusArgs) (*mcp.CallToolResult, any, error) {
		principal, _, err := bridge.MCPPrincipal()
		if err != nil {
			return toolErr(err), nil, nil
		}
		job, err := bridge.TaskStatus(principal, a.JobID, a.Token)
		if err != nil {
			return toolErr(err), nil, nil
		}
		return nil, job, nil
	})
	if err := mcp.AddReceivingCustomMethod(s, "events/list", func(ctx context.Context, ss *mcp.ServerSession, params *EventsListParams) (*EventsListResult, error) {
		principal, _, err := bridge.MCPPrincipal()
		if err != nil {
			return nil, err
		}
		return &EventsListResult{Events: bridge.EventsList(principal)}, nil
	}); err != nil {
		return nil, err
	}
	if err := mcp.AddReceivingCustomMethod(s, "events/subscribe", func(ctx context.Context, ss *mcp.ServerSession, params *EventsSubscribeParams) (*EventsSubscribeResult, error) {
		principal, queueID, err := bridge.MCPPrincipal()
		if err != nil {
			return nil, err
		}
		if params.Params.Arguments.QueueID != queueID {
			return nil, fmt.Errorf("queue_mismatch")
		}
		sub, err := bridge.EventsSubscribe(ctx, principal, params.Params)
		if err != nil {
			return nil, err
		}
		return &EventsSubscribeResult{Subscription: sub}, nil
	}); err != nil {
		return nil, err
	}
	if err := mcp.AddReceivingCustomMethod(s, "events/unsubscribe", func(ctx context.Context, ss *mcp.ServerSession, params *EventsUnsubscribeParams) (*EventsUnsubscribeResult, error) {
		principal, queueID, err := bridge.MCPPrincipal()
		if err != nil {
			return nil, err
		}
		if params.Params.Arguments.QueueID != queueID {
			return nil, fmt.Errorf("queue_mismatch")
		}
		if err := bridge.EventsUnsubscribe(ctx, principal, params.Params); err != nil {
			return nil, err
		}
		return &EventsUnsubscribeResult{}, nil
	}); err != nil {
		return nil, err
	}
	return s, nil
}

func toolErr(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprint(err)}}}
}

// HTTPWithBridge serves MCP with optional events capability advertisement.
func HTTPWithBridge(backend service.Backend, bridge *dotbridge.Service) (http.Handler, error) {
	s, err := NewWithBridge(backend, bridge)
	if err != nil {
		return nil, err
	}
	base := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return DiscoverEventsWrapper(base), nil
}

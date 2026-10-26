package runtime

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/docker/docker-agent/pkg/runtime/sampling"
)

func (r *LocalRuntime) samplingHandler(ctx context.Context, req *mcp.CreateMessageParams) (*mcp.CreateMessageResult, error) {
	return (sampling.Handler{Agent: r.CurrentAgent}).Handle(ctx, req)
}

func (r *LocalRuntime) samplingWithToolsHandler(ctx context.Context, req *mcp.CreateMessageWithToolsParams) (*mcp.CreateMessageWithToolsResult, error) {
	return (sampling.Handler{Agent: r.CurrentAgent}).HandleWithTools(ctx, req)
}

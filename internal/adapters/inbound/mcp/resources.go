package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mimeTypeJSON is the MIME type of every resource this server serves.
const mimeTypeJSON = "application/json"

// Resource URI templates (Agentic Standard v2, AS-3; docs/adr/0007): SCOPED
// views this context owns, never a table dump. Each is backed by the same
// read use case as its tool and REST route; the concrete id is read from the
// request URI at call time.
const (
	boardResourceTemplate   = "warehouse://yard/sites/{siteId}/board"
	trailerResourceTemplate = "warehouse://yard/trailers/{trailerId}"

	boardResourcePrefix   = "warehouse://yard/sites/"
	boardResourceSuffix   = "/board"
	trailerResourcePrefix = "warehouse://yard/trailers/"
)

// registerResources adds the scoped read-model resources. Resources are L1 by
// nature (pure reads) and, per the charter, need no tool Meta.
func (d Deps) registerResources(server *mcp.Server) {
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: boardResourceTemplate,
		Name:        "yard-board",
		Description: "The live yard board of one site: spots and doors with the trailer at each, trailers in motion and open moves.",
		MIMEType:    mimeTypeJSON,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		siteID, err := parseResourceID(req.Params.URI, boardResourcePrefix, boardResourceSuffix, boardResourceTemplate)
		if err != nil {
			return nil, err
		}
		board, err := d.getYardBoard(ctx, siteInput{SiteID: siteID})
		return resourceJSON(req.Params.URI, board, err)
	})

	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: trailerResourceTemplate,
		Name:        "trailer",
		Description: "One trailer by id: location, load state, door work, seal, hold, visit, appointment and open move.",
		MIMEType:    mimeTypeJSON,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		trailerID, err := parseResourceID(req.Params.URI, trailerResourcePrefix, "", trailerResourceTemplate)
		if err != nil {
			return nil, err
		}
		t, err := d.getTrailer(ctx, trailerIDInput{TrailerID: trailerID})
		return resourceJSON(req.Params.URI, t, err)
	})
}

// resourceJSON wraps a read model as the one JSON content of a resource.
func resourceJSON(uri string, v any, err error) (*mcp.ReadResourceResult, error) {
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: mimeTypeJSON, Text: string(body)}},
	}, nil
}

// parseResourceID extracts the id segment of a resource URI. The segment is
// model-supplied and therefore untrusted; a malformed URI or an empty or
// nested segment is rejected rather than silently defaulted.
func parseResourceID(uri, prefix, suffix, template string) (string, error) {
	id, ok := strings.CutPrefix(uri, prefix)
	if ok && suffix != "" {
		id, ok = strings.CutSuffix(id, suffix)
	}
	if !ok || id == "" || strings.Contains(id, "/") {
		return "", fmt.Errorf("resource uri %q must match %s", uri, template)
	}
	return id, nil
}

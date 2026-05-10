package auth

import (
	"context"
	"errors"
	"net/http"
)

// Bearer reads data["token"] (string) and sets
// `Authorization: Bearer <token>` on the request.
//
// Expected JSON shape inside [mcpx.ServerConfig.Auth]:
//
//	{"auth": {"token": "..."}}
//
// Returns an error if data["token"] is missing, not a string, or empty.
//
// Compatible with [mcpx.AuthFunc]:
//
//	mcpx.New(ctx, cfg, mcpx.WithAuthFunc(auth.Bearer))
func Bearer(_ context.Context, _ string, r *http.Request, data map[string]any) error {
	tok, ok := data["token"].(string)
	if !ok {
		if _, present := data["token"]; present {
			return errors.New(`auth.Bearer: data["token"] is not a string`)
		}
		return errors.New(`auth.Bearer: missing data["token"]`)
	}
	if tok == "" {
		return errors.New(`auth.Bearer: empty data["token"]`)
	}
	r.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

// HeaderToken reads data["tokenName"] (header name, string) and
// data["value"] (raw token, string) and sets that header on the request
// verbatim — no `Bearer ` prefix.
//
// Expected JSON shape inside [mcpx.ServerConfig.Auth]:
//
//	{"auth": {"tokenName": "X-MCP-AUTH", "value": "..."}}
//
// Returns an error if any required key is missing, not a string, or empty.
//
// Compatible with [mcpx.AuthFunc]:
//
//	mcpx.New(ctx, cfg, mcpx.WithAuthFunc(auth.HeaderToken))
func HeaderToken(_ context.Context, _ string, r *http.Request, data map[string]any) error {
	name, ok := data["tokenName"].(string)
	if !ok {
		if _, present := data["tokenName"]; present {
			return errors.New(`auth.HeaderToken: data["tokenName"] is not a string`)
		}
		return errors.New(`auth.HeaderToken: missing data["tokenName"]`)
	}
	if name == "" {
		return errors.New(`auth.HeaderToken: empty data["tokenName"]`)
	}
	val, ok := data["value"].(string)
	if !ok {
		if _, present := data["value"]; present {
			return errors.New(`auth.HeaderToken: data["value"] is not a string`)
		}
		return errors.New(`auth.HeaderToken: missing data["value"]`)
	}
	if val == "" {
		return errors.New(`auth.HeaderToken: empty data["value"]`)
	}
	r.Header.Set(name, val)
	return nil
}

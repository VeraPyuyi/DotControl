package codexbridge

import "context"

type PermissionProfile struct {
	ID          string `json:"id"`
	Allowed     bool   `json:"allowed"`
	Description string `json:"description,omitempty"`
}

// ReadPermissionProfiles lists effective profiles without changing permissions.
func ReadPermissionProfiles(ctx context.Context, c *Client, cwd string) ([]PermissionProfile, error) {
	out := []PermissionProfile{}
	seen := map[string]bool{}
	cursor := ""
	for range 100 {
		params := map[string]any{"cwd": cwd, "limit": 100}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Data       []PermissionProfile `json:"data"`
			NextCursor string              `json:"nextCursor"`
		}
		if err := c.Call(ctx, "permissionProfile/list", params, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Data...)
		if page.NextCursor == "" {
			return out, nil
		}
		if seen[page.NextCursor] {
			return nil, failure("codex_inventory_incomplete")
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	return nil, failure("codex_inventory_incomplete")
}

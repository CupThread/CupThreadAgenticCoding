package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

func newAPICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Low-level API access (for agents and unreleased endpoints)",
	}
	cmd.AddCommand(newAPIRequestCmd())
	return cmd
}

func newAPIRequestCmd() *cobra.Command {
	var inputPath string
	req := &cobra.Command{
		Use:   "request <METHOD> <path>",
		Short: "Perform a raw authenticated API request",
		Long: `Perform a raw authenticated request against the API.

Path must start with "/" and is appended to the base URL, e.g.
  cupthread api request GET /api/v1/console/me

Authentication, the X-Workspace-Id header (when a workspace is resolved) and
JSON output are handled the same as the high-level commands. Pass a JSON body
with --input @file (or "-" for stdin). This is the escape hatch for endpoints
the CLI does not wrap yet.`,
		Args:                  cobra.ExactArgs(2),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			method := upper(args[0])
			path := args[1]
			if len(path) == 0 || path[0] != '/' {
				return errors.New("path must start with '/'")
			}

			var body any
			if inputPath != "" {
				data, err := readInputFile(inputPath)
				if err != nil {
					return err
				}
				dec := json.NewDecoder(bytes.NewReader(data))
				if err := dec.Decode(&body); err != nil {
					return fmt.Errorf("parse input JSON: %w", err)
				}
			}

			var raw json.RawMessage
			if err := A.client.Do(cmd.Context(), method, path, nil, body, &raw); err != nil {
				// Still surface structured API errors as JSON when in JSON mode.
				if apiErr, ok := err.(*api.APIError); ok && A.structured() {
					return A.out.Structured(map[string]any{
						"error":   apiErr.Message,
						"code":    apiErr.Code,
						"status":  apiErr.Status,
					})
				}
				return err
			}

			if A.structured() {
				return A.out.Structured(raw)
			}
			if len(raw) == 0 {
				A.out.Printf("✓ %s %s succeeded (no response body)", method, path)
				return nil
			}
			A.out.Printf("✓ %s %s", method, path)
			A.out.Printf("%s", raw)
			return nil
		},
	}
	req.Flags().StringVar(&inputPath, "input", "", "JSON request body file (\"-\" or \"@\" for stdin)")
	return req
}

func upper(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'a' && c <= 'z' {
			out[i] = c - 32
		}
	}
	return string(out)
}

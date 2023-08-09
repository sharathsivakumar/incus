//go:build linux && cgo && !agent

package cluster

// The code below was generated by incus-generate - DO NOT EDIT!

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/lxc/incus/shared/api"
)

var _ = api.ServerEnvironment{}

var nodeID = RegisterStmt(`
SELECT nodes.id FROM nodes
  WHERE nodes.name = ?
`)

// GetNodeID return the ID of the node with the given key.
// generator: node ID
func GetNodeID(ctx context.Context, tx *sql.Tx, name string) (int64, error) {
	stmt, err := Stmt(tx, nodeID)
	if err != nil {
		return -1, fmt.Errorf("Failed to get \"nodeID\" prepared statement: %w", err)
	}

	row := stmt.QueryRowContext(ctx, name)
	var id int64
	err = row.Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return -1, api.StatusErrorf(http.StatusNotFound, "Node not found")
	}

	if err != nil {
		return -1, fmt.Errorf("Failed to get \"nodes\" ID: %w", err)
	}

	return id, nil
}

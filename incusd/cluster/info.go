package cluster

import (
	"context"
	"os"
	"path/filepath"

	"github.com/lxc/incus/incusd/db"
	"github.com/lxc/incus/incusd/node"
	"github.com/lxc/incus/shared"
	"github.com/lxc/incus/shared/logger"
	localtls "github.com/lxc/incus/shared/tls"
)

// Load information about the dqlite node associated with this cluster member.
func loadInfo(database *db.Node, cert *localtls.CertInfo) (*db.RaftNode, error) {
	// Figure out if we actually need to act as dqlite node.
	var info *db.RaftNode
	err := database.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		var err error
		info, err = node.DetermineRaftNode(ctx, tx)
		return err
	})
	if err != nil {
		return nil, err
	}

	// If we're not part of the dqlite cluster, there's nothing to do.
	if info == nil {
		return nil, nil
	}

	if info.Address == "" {
		// This is a standalone node not exposed to the network.
		info.Address = "1"
	}

	logger.Info("Starting database node", logger.Ctx{"id": info.ID, "local": info.Address, "role": info.Role})

	// Data directory
	dir := filepath.Join(database.Dir(), "global")
	if !shared.PathExists(dir) {
		err := os.Mkdir(dir, 0750)
		if err != nil {
			return nil, err
		}
	}

	return info, nil
}

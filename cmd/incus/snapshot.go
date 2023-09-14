package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/client"
	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/i18n"
	"github.com/lxc/incus/shared"
	"github.com/lxc/incus/shared/api"
)

type cmdSnapshot struct {
	global *cmdGlobal
}

func (c *cmdSnapshot) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("snapshot")
	cmd.Short = i18n.G("Manage instance snapshots")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage instance snapshots`))

	// Create.
	snapshotCreateCmd := cmdSnapshotCreate{global: c.global, snapshot: c}
	cmd.AddCommand(snapshotCreateCmd.Command())

	// Delete.
	snapshotDeleteCmd := cmdSnapshotDelete{global: c.global, snapshot: c}
	cmd.AddCommand(snapshotDeleteCmd.Command())

	// List.
	snapshotListCmd := cmdSnapshotList{global: c.global, snapshot: c}
	cmd.AddCommand(snapshotListCmd.Command())

	// Rename.
	snapshotRenameCmd := cmdSnapshotRename{global: c.global, snapshot: c}
	cmd.AddCommand(snapshotRenameCmd.Command())

	// Restore.
	snapshotRestoreCmd := cmdSnapshotRestore{global: c.global, snapshot: c}
	cmd.AddCommand(snapshotRestoreCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdSnapshotCreate struct {
	global   *cmdGlobal
	snapshot *cmdSnapshot

	flagStateful bool
	flagNoExpiry bool
	flagReuse    bool
}

func (c *cmdSnapshotCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<instance> [<snapshot name>]"))
	cmd.Short = i18n.G("Create instance snapshot")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create instance snapshots

When --stateful is used, attempt to checkpoint the instance's
running state, including process memory state, TCP connections, ...`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus snapshot create u1 snap0
    Create a snapshot of "u1" called "snap0".`))

	cmd.Flags().BoolVar(&c.flagStateful, "stateful", false, i18n.G("Whether or not to snapshot the instance's running state"))
	cmd.Flags().BoolVar(&c.flagNoExpiry, "no-expiry", false, i18n.G("Ignore any configured auto-expiry for the instance"))
	cmd.Flags().BoolVar(&c.flagReuse, "reuse", false, i18n.G("If the snapshot name already exists, delete and create a new one"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdSnapshotCreate) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	var snapname string
	if len(args) < 2 {
		snapname = ""
	} else {
		snapname = args[1]
	}

	remote, name, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	if shared.IsSnapshot(name) {
		if snapname == "" {
			fields := strings.SplitN(name, shared.SnapshotDelimiter, 2)
			name = fields[0]
			snapname = fields[1]
		} else {
			return fmt.Errorf(i18n.G("Invalid instance name: %s"), name)
		}
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	if c.flagReuse && snapname != "" {
		snap, _, _ := d.GetInstanceSnapshot(name, snapname)
		if snap != nil {
			op, err := d.DeleteInstanceSnapshot(name, snapname)
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return err
			}
		}
	}

	req := api.InstanceSnapshotsPost{
		Name:     snapname,
		Stateful: c.flagStateful,
	}

	if c.flagNoExpiry {
		req.ExpiresAt = &time.Time{}
	}

	op, err := d.CreateInstanceSnapshot(name, req)
	if err != nil {
		return err
	}

	return op.Wait()
}

// Delete.
type cmdSnapshotDelete struct {
	global   *cmdGlobal
	snapshot *cmdSnapshot

	flagInteractive bool
}

func (c *cmdSnapshotDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<instance>/<snapshot name> [[<remote>:]<instance>/<snapshot name>...]"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete instance snapshots")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete instance snapshots`))

	cmd.Flags().BoolVarP(&c.flagInteractive, "interactive", "i", false, i18n.G("Require user confirmation"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdSnapshotDelete) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args...)
	if err != nil {
		return err
	}

	// Check that everything exists.
	err = instancesExist(resources)
	if err != nil {
		return err
	}

	// Process with deletion.
	for _, resource := range resources {
		if c.flagInteractive {
			err := c.promptDelete(resource.name)
			if err != nil {
				return err
			}
		}

		err := c.doDelete(resource.server, resource.name)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *cmdSnapshotDelete) promptDelete(name string) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf(i18n.G("Remove %s (yes/no): "), name)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSuffix(input, "\n")

	if !shared.StringInSlice(strings.ToLower(input), []string{i18n.G("yes")}) {
		return fmt.Errorf(i18n.G("User aborted delete operation"))
	}

	return nil
}

func (c *cmdSnapshotDelete) doDelete(d incus.InstanceServer, name string) error {
	var op incus.Operation
	var err error

	// Snapshot delete
	fields := strings.SplitN(name, shared.SnapshotDelimiter, 2)

	op, err = d.DeleteInstanceSnapshot(fields[0], fields[1])
	if err != nil {
		return err
	}

	return op.Wait()
}

// List.
type cmdSnapshotList struct {
	global   *cmdGlobal
	snapshot *cmdSnapshot

	flagFormat string
}

func (c *cmdSnapshotList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]<instance>"))
	cmd.Short = i18n.G("List instance snapshots")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List instance snapshots`))

	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdSnapshotList) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	remote, instanceName, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	return c.listSnapshots(d, instanceName)
}

func (c *cmdSnapshotList) listSnapshots(d incus.InstanceServer, name string) error {
	snapshots, err := d.GetInstanceSnapshots(name)
	if err != nil {
		return err
	}

	const layout = "2006/01/02 15:04 MST"

	// List snapshots
	firstSnapshot := true
	snapData := [][]string{}

	for _, snap := range snapshots {
		if firstSnapshot {
			fmt.Println("\n" + i18n.G("Snapshots:"))
		}

		var row []string

		fields := strings.Split(snap.Name, shared.SnapshotDelimiter)
		row = append(row, fields[len(fields)-1])

		if shared.TimeIsSet(snap.CreatedAt) {
			row = append(row, snap.CreatedAt.Local().Format(layout))
		} else {
			row = append(row, " ")
		}

		if shared.TimeIsSet(snap.ExpiresAt) {
			row = append(row, snap.ExpiresAt.Local().Format(layout))
		} else {
			row = append(row, " ")
		}

		if snap.Stateful {
			row = append(row, "YES")
		} else {
			row = append(row, "NO")
		}

		firstSnapshot = false
		snapData = append(snapData, row)
	}

	snapHeader := []string{
		i18n.G("Name"),
		i18n.G("Taken at"),
		i18n.G("Expires at"),
		i18n.G("Stateful"),
	}

	_ = cli.RenderTable(cli.TableFormatTable, snapHeader, snapData, snapshots)

	return nil
}

// Rename.
type cmdSnapshotRename struct {
	global   *cmdGlobal
	snapshot *cmdSnapshot
}

func (c *cmdSnapshotRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<instance> <old snapshot name> <new snapshot name>"))
	cmd.Short = i18n.G("Rename instance snapshots")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename instance snapshots`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdSnapshotRename) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	// Check the remotes
	remote, instanceName, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	// Snapshot rename
	op, err := d.RenameInstanceSnapshot(instanceName, args[1], api.InstanceSnapshotPost{Name: args[2]})
	if err != nil {
		return err
	}

	return op.Wait()
}

// Restore.
type cmdSnapshotRestore struct {
	global   *cmdGlobal
	snapshot *cmdSnapshot

	flagStateful bool
}

func (c *cmdSnapshotRestore) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("restore", i18n.G("[<remote>:]<instance> <snapshot name>"))
	cmd.Short = i18n.G("Restore instance snapshots")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Restore instance from snapshots

If --stateful is passed, then the running state will be restored too.`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus snapshot create u1 snap0
Create the snapshot.

incus snapshot restore u1 snap0
Restore the snapshot.`))

	cmd.Flags().BoolVar(&c.flagStateful, "stateful", false, i18n.G("Whether or not to restore the instance's running state from snapshot (if available)"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdSnapshotRestore) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Connect to the daemon.
	remote, name, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	// Setup the snapshot restore
	snapname := args[1]
	if !shared.IsSnapshot(snapname) {
		snapname = fmt.Sprintf("%s/%s", name, snapname)
	}

	req := api.InstancePut{
		Restore:  snapname,
		Stateful: c.flagStateful,
	}

	// Restore the snapshot
	op, err := d.UpdateInstance(name, req, "")
	if err != nil {
		return err
	}

	return op.Wait()
}

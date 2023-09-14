package main

import (
	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/i18n"
	"github.com/spf13/cobra"
)

type cmdAdmin struct {
	global *cmdGlobal
}

func (c *cmdAdmin) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("admin")
	cmd.Short = i18n.G("Manage incus deamon")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage incus daemon`))

	// init
	adminInitCmd := cmdAdminInit{global: c.global}
	cmd.AddCommand(adminInitCmd.Command())

	return cmd
}

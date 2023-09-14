package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	lxdAPI "github.com/canonical/lxd/shared/api"
	"github.com/spf13/cobra"

	"github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/version"
	incusAPI "github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/subprocess"
)

var minLXDVersion = &version.DottedVersion{4, 0, 0}
var maxLXDVersion = &version.DottedVersion{5, 16, 0}

type cmdGlobal struct {
	flagHelp    bool
	flagVersion bool
}

func main() {
	// Setup command line parser.
	daemonCmd := cmdMigrate{}

	app := daemonCmd.Command()
	app.Use = "lxd-to-incus"
	app.Short = "LXD to Incus migration tool"
	app.Long = `Description:
  LXD to Incus migration tool

  This tool allows an existing LXD user to move all their data over to Incus.
`
	app.SilenceUsage = true
	app.CompletionOptions = cobra.CompletionOptions{DisableDefaultCmd: true}

	// Global flags.
	globalCmd := cmdGlobal{}
	app.PersistentFlags().BoolVar(&globalCmd.flagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&globalCmd.flagHelp, "help", "h", false, "Print help")

	// Version handling.
	app.SetVersionTemplate("{{.Version}}\n")
	app.Version = version.Version

	// Run the main command and handle errors.
	err := app.Execute()
	if err != nil {
		os.Exit(1)
	}
}

type cmdMigrate struct {
	flagYes bool
}

func (c *cmdMigrate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "lxd-to-incus"
	cmd.RunE = c.Run
	cmd.PersistentFlags().BoolVar(&c.flagYes, "yes", false, "Migrate without prompting")

	return cmd
}

func (c *cmdMigrate) Run(app *cobra.Command, args []string) error {
	// Confirm that we're root.
	if os.Geteuid() != 0 {
		return fmt.Errorf("This tool must be run as root")
	}

	// Iterate through potential sources.
	fmt.Println("=> Looking for source server")
	var source Source
	for _, candidate := range sources {
		if !candidate.Present() {
			continue
		}

		source = candidate
		break
	}

	if source == nil {
		return fmt.Errorf("No source server could be found")
	}

	// Iterate through potential targets.
	fmt.Println("=> Looking for target server")
	var target Target
	for _, candidate := range targets {
		if !candidate.Present() {
			continue
		}

		target = candidate
		break
	}

	if target == nil {
		return fmt.Errorf("No target server could be found")
	}

	// Connect to the servers.
	fmt.Println("=> Connecting to source server")
	srcClient, err := source.Connect()
	if err != nil {
		return fmt.Errorf("Failed to connect to the source: %w", err)
	}

	fmt.Println("=> Connecting to the target server")
	targetClient, err := target.Connect()
	if err != nil {
		return fmt.Errorf("Failed to connect to the target: %w", err)
	}

	// Get versions.
	fmt.Println("=> Checking server versions")
	srcServerInfo, _, err := srcClient.GetServer()
	if err != nil {
		return fmt.Errorf("Failed getting source server info: %w", err)
	}

	targetServerInfo, _, err := targetClient.GetServer()
	if err != nil {
		return fmt.Errorf("Failed getting target server info: %w", err)
	}

	fmt.Printf("==> Source version: %s\n", srcServerInfo.Environment.ServerVersion)
	fmt.Printf("==> Target version: %s\n", targetServerInfo.Environment.ServerVersion)

	// Compare versions.
	fmt.Println("=> Validating version compatibility")
	srcVersion, err := version.Parse(srcServerInfo.Environment.ServerVersion)
	if err != nil {
		return fmt.Errorf("Couldn't parse source server version: %w", err)
	}

	targetVersion, err := version.Parse(targetServerInfo.Environment.ServerVersion)
	if err != nil {
		return fmt.Errorf("Couldn't parse target server version: %w", err)
	}

	if srcVersion.Compare(minLXDVersion) < 0 {
		return fmt.Errorf("LXD version is lower than minimal version %q", minLXDVersion)
	}

	if srcVersion.Compare(maxLXDVersion) > 0 {
		return fmt.Errorf("LXD version is newer than maximum version %q", maxLXDVersion)
	}

	if srcVersion.Compare(targetVersion) > 0 {
		return fmt.Errorf("Incus version is older than source LXD version")
	}

	// Validate source non-empty.
	srcCheckEmpty := func() (bool, error) {
		// Check if more than one project.
		names, err := srcClient.GetProjectNames()
		if err != nil {
			return false, err
		}

		if len(names) > 1 {
			return false, nil
		}

		// Check if more than one profile.
		names, err = srcClient.GetProfileNames()
		if err != nil {
			return false, err
		}

		if len(names) > 1 {
			return false, nil
		}

		// Check if any instance is persent.
		names, err = srcClient.GetInstanceNames(lxdAPI.InstanceTypeAny)
		if err != nil {
			return false, err
		}

		if len(names) > 0 {
			return false, nil
		}

		// Check if any storage pool is present.
		names, err = srcClient.GetStoragePoolNames()
		if err != nil {
			return false, err
		}

		if len(names) > 0 {
			return false, nil
		}

		// Check if any network is present.
		networks, err := srcClient.GetNetworks()
		if err != nil {
			return false, err
		}

		for _, network := range networks {
			if network.Managed {
				return false, nil
			}
		}

		return true, nil
	}

	fmt.Println("=> Checking that the source server isn't empty")
	isEmpty, err := srcCheckEmpty()
	if err != nil {
		return fmt.Errorf("Failed to check source server: %w", err)
	}

	if isEmpty {
		return fmt.Errorf("Source server is empty, migration not needed")
	}

	// Validate target empty.
	targetCheckEmpty := func() (bool, error) {
		// Check if more than one project.
		names, err := targetClient.GetProjectNames()
		if err != nil {
			return false, err
		}

		if len(names) > 1 {
			return false, nil
		}

		// Check if more than one profile.
		names, err = targetClient.GetProfileNames()
		if err != nil {
			return false, err
		}

		if len(names) > 1 {
			return false, nil
		}

		// Check if any instance is present.
		names, err = targetClient.GetInstanceNames(incusAPI.InstanceTypeAny)
		if err != nil {
			return false, err
		}

		if len(names) > 0 {
			return false, nil
		}

		// Check if any storage pool is present.
		names, err = targetClient.GetStoragePoolNames()
		if err != nil {
			return false, err
		}

		if len(names) > 0 {
			return false, nil
		}

		// Check if any network is present.
		networks, err := targetClient.GetNetworks()
		if err != nil {
			return false, err
		}

		for _, network := range networks {
			if network.Managed {
				return false, nil
			}
		}

		return true, nil
	}

	fmt.Println("=> Checking that the target server is empty")
	isEmpty, err = targetCheckEmpty()
	if err != nil {
		return fmt.Errorf("Failed to check target server: %w", err)
	}

	if !isEmpty {
		return fmt.Errorf("Target server isn't empty, can't proceed with migration.")
	}

	// Validate configuration.
	fmt.Println("=> Validating source server configuration")
	deprecatedConfigs := []string{
		"candid.api.key",
		"candid.api.url",
		"candid.domains",
		"candid.expiry",
		"core.trust_password",
		"maas.api.key",
		"maas.api.url",
		"rbac.agent.url",
		"rbac.agent.username",
		"rbac.agent.private_key",
		"rbac.agent.public_key",
		"rbac.api.expiry",
		"rbac.api.key",
		"rbac.api.url",
		"rbac.expiry",
	}

	for _, key := range deprecatedConfigs {
		_, ok := srcServerInfo.Config[key]
		if ok {
			return fmt.Errorf("Source server is using deprecated key %q, please unset and retry.", key)
		}
	}

	// Confirm migration.
	if !c.flagYes {
		fmt.Println(`
The migration is now ready to proceed.
At this point, the source server and all its instances will be stopped.
Instances will come back online once the migration is complete.
`)

		ok, err := cmd.AskBool("Proceed with the migration? [default=no]: ", "no")
		if err != nil {
			return err
		}

		if !ok {
			os.Exit(1)
		}
	}

	// Stop source.
	fmt.Println("=> Stopping the source server")
	err = source.Stop()
	if err != nil {
		fmt.Errorf("Failed to stop the source server: %w", err)
	}

	// Stop target.
	fmt.Println("=> Stopping the target server")
	err = target.Stop()
	if err != nil {
		fmt.Errorf("Failed to stop the target server: %w", err)
	}

	// Wipe the target.
	fmt.Println("=> Wiping the target server")
	targetPaths, err := target.Paths()
	if err != nil {
		return fmt.Errorf("Failed to get target paths: %w", err)
	}

	err = os.RemoveAll(targetPaths.Logs)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to remove %q: %w", targetPaths.Logs, err)
	}

	err = os.RemoveAll(targetPaths.Cache)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to remove %q: %w", targetPaths.Cache, err)
	}

	err = os.RemoveAll(targetPaths.Daemon)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to remove %q: %w", targetPaths.Daemon, err)
	}

	// Migrate data.
	fmt.Println("=> Migrating the data")
	sourcePaths, err := source.Paths()
	if err != nil {
		return fmt.Errorf("Failed to get source paths: %w", err)
	}

	_, err = subprocess.RunCommand("mv", sourcePaths.Logs, targetPaths.Logs)
	if err != nil {
		return fmt.Errorf("Failed to move %q to %q: %w", sourcePaths.Logs, targetPaths.Logs, err)
	}

	_, err = subprocess.RunCommand("mv", sourcePaths.Cache, targetPaths.Cache)
	if err != nil {
		return fmt.Errorf("Failed to move %q to %q: %w", sourcePaths.Cache, targetPaths.Cache, err)
	}

	_, err = subprocess.RunCommand("mv", sourcePaths.Daemon, targetPaths.Daemon)
	if err != nil {
		return fmt.Errorf("Failed to move %q to %q: %w", sourcePaths.Daemon, targetPaths.Daemon, err)
	}

	// Migrate database format.
	fmt.Println("=> Migrating database")
	err = migrateDatabase(filepath.Join(targetPaths.Daemon, "database"))
	if err != nil {
		return fmt.Errorf("Failed to migrate database in %q: %w", filepath.Join(targetPaths.Daemon, "database"), err)
	}

	// Cleanup paths.
	fmt.Println("=> Cleaning up target paths")

	for _, dir := range []string{"devices", "devlxd", "security", "shmounts"} {
		err = os.RemoveAll(filepath.Join(targetPaths.Daemon, dir))
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("Failed to delete %q: %w", dir, err)
		}
	}

	for _, dir := range []string{"containers", "containers-snapshots", "snapshots", "virtual-machines", "virtual-machines-snapshots"} {
		entries, err := ioutil.ReadDir(filepath.Join(targetPaths.Daemon, dir))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return fmt.Errorf("Failed to read entries in %q: %w", filepath.Join(targetPaths.Daemon, dir), err)
		}

		for _, entry := range entries {
			srcPath := filepath.Join(targetPaths.Daemon, dir, entry.Name())
			oldTarget, err := os.Readlink(srcPath)
			if err != nil {
				return fmt.Errorf("Failed to resolve symlink %q: %w", srcPath, err)
			}

			newTarget := strings.Replace(oldTarget, sourcePaths.Daemon, targetPaths.Daemon, 1)
			err = os.Remove(srcPath)
			if err != nil {
				return fmt.Errorf("Failed to delete symlink %q: %w", srcPath, err)
			}

			err = os.Symlink(newTarget, srcPath)
			if err != nil {
				return fmt.Errorf("Failed to create symlink %q: %w", srcPath, err)
			}
		}
	}

	// Start target.
	fmt.Println("=> Starting the target server")
	err = target.Start()
	if err != nil {
		return fmt.Errorf("Failed to start the target server: %w", err)
	}

	// Validate target.
	fmt.Println("=> Checking the target server")
	_, _, err = targetClient.GetServer()
	if err != nil {
		return fmt.Errorf("Failed to get target server info: %w", err)
	}

	// Confirm uninstall.
	if !c.flagYes {
		ok, err := cmd.AskBool("Uninstall the LXD package? [default=no]: ", "no")
		if err != nil {
			return err
		}

		if !ok {
			os.Exit(1)
		}
	}

	// Purge source.
	fmt.Println("=> Uninstalling the source server")
	err = source.Purge()
	if err != nil {
		return fmt.Errorf("Failed to uninstall the source server: %w", err)
	}

	return nil
}

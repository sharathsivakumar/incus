package main

import (
	"encoding/pem"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/incusd/revert"
	"github.com/lxc/incus/incusd/util"
	"github.com/lxc/incus/internal/ports"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared"
	"github.com/lxc/incus/shared/api"
	localtls "github.com/lxc/incus/shared/tls"
)

type cmdInit struct {
	global *cmdGlobal

	flagAuto    bool
	flagMinimal bool
	flagPreseed bool
	flagDump    bool

	flagNetworkAddress  string
	flagNetworkPort     int
	flagStorageBackend  string
	flagStorageDevice   string
	flagStorageLoopSize int
	flagStoragePool     string

	hostname string
}

func (c *cmdInit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "init"
	cmd.Short = "Configure the daemon"
	cmd.Long = `Description:
  Configure the daemon
`
	cmd.Example = `  init --minimal
  init --auto [--network-address=IP] [--network-port=8443] [--storage-backend=dir]
              [--storage-create-device=DEVICE] [--storage-create-loop=SIZE]
              [--storage-pool=POOL]
  init --preseed
  init --dump
`
	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagAuto, "auto", false, "Automatic (non-interactive) mode")
	cmd.Flags().BoolVar(&c.flagMinimal, "minimal", false, "Minimal configuration (non-interactive)")
	cmd.Flags().BoolVar(&c.flagPreseed, "preseed", false, "Pre-seed mode, expects YAML config from stdin")
	cmd.Flags().BoolVar(&c.flagDump, "dump", false, "Dump YAML config to stdout")

	cmd.Flags().StringVar(&c.flagNetworkAddress, "network-address", "", "Address to bind to (default: none)"+"``")
	cmd.Flags().IntVar(&c.flagNetworkPort, "network-port", -1, fmt.Sprintf("Port to bind to (default: %d)"+"``", ports.HTTPSDefaultPort))
	cmd.Flags().StringVar(&c.flagStorageBackend, "storage-backend", "", "Storage backend to use (btrfs, dir, lvm or zfs, default: dir)"+"``")
	cmd.Flags().StringVar(&c.flagStorageDevice, "storage-create-device", "", "Setup device based storage using DEVICE"+"``")
	cmd.Flags().IntVar(&c.flagStorageLoopSize, "storage-create-loop", -1, "Setup loop based storage with SIZE in GB"+"``")
	cmd.Flags().StringVar(&c.flagStoragePool, "storage-pool", "", "Storage pool to use or create"+"``")

	return cmd
}

func (c *cmdInit) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	if c.flagAuto && c.flagPreseed {
		return fmt.Errorf("Can't use --auto and --preseed together")
	}

	if c.flagMinimal && c.flagPreseed {
		return fmt.Errorf("Can't use --minimal and --preseed together")
	}

	if c.flagMinimal && c.flagAuto {
		return fmt.Errorf("Can't use --minimal and --auto together")
	}

	if !c.flagAuto && (c.flagNetworkAddress != "" || c.flagNetworkPort != -1 ||
		c.flagStorageBackend != "" || c.flagStorageDevice != "" ||
		c.flagStorageLoopSize != -1 || c.flagStoragePool != "") {
		return fmt.Errorf("Configuration flags require --auto")
	}

	if c.flagDump && (c.flagAuto || c.flagMinimal ||
		c.flagPreseed || c.flagNetworkAddress != "" ||
		c.flagNetworkPort != -1 || c.flagStorageBackend != "" ||
		c.flagStorageDevice != "" || c.flagStorageLoopSize != -1 ||
		c.flagStoragePool != "") {
		return fmt.Errorf("Can't use --dump with other flags")
	}

	// Connect to the daemon
	d, err := incus.ConnectIncusUnix("", nil)
	if err != nil {
		return fmt.Errorf("Failed to connect to local daemon: %w", err)
	}

	server, _, err := d.GetServer()
	if err != nil {
		return fmt.Errorf("Failed to connect to get server info: %w", err)
	}

	// Dump mode
	if c.flagDump {
		err := c.RunDump(d)
		if err != nil {
			return err
		}

		return nil
	}

	// Prepare the input data
	var config *api.InitPreseed

	// Preseed mode
	if c.flagPreseed {
		config, err = c.RunPreseed(cmd, args, d)
		if err != nil {
			return err
		}
	}

	// Auto mode
	if c.flagAuto || c.flagMinimal {
		config, err = c.RunAuto(cmd, args, d, server)
		if err != nil {
			return err
		}
	}

	// Interactive mode
	if !c.flagAuto && !c.flagMinimal && !c.flagPreseed {
		config, err = c.RunInteractive(cmd, args, d, server)
		if err != nil {
			return err
		}
	}

	// Check if the path to the cluster certificate is set
	// If yes then read cluster certificate from file
	if config.Cluster != nil && config.Cluster.ClusterCertificatePath != "" {
		if !shared.PathExists(config.Cluster.ClusterCertificatePath) {
			return fmt.Errorf("Path %s doesn't exist", config.Cluster.ClusterCertificatePath)
		}

		content, err := os.ReadFile(config.Cluster.ClusterCertificatePath)
		if err != nil {
			return err
		}

		config.Cluster.ClusterCertificate = string(content)
	}

	// Check if we got a cluster join token, if so, fill in the config with it.
	if config.Cluster != nil && config.Cluster.ClusterToken != "" {
		joinToken, err := shared.JoinTokenDecode(config.Cluster.ClusterToken)
		if err != nil {
			return fmt.Errorf("Invalid cluster join token: %w", err)
		}

		// Set server name from join token
		config.Cluster.ServerName = joinToken.ServerName

		// Attempt to find a working cluster member to use for joining by retrieving the
		// cluster certificate from each address in the join token until we succeed.
		for _, clusterAddress := range joinToken.Addresses {
			// Cluster URL
			config.Cluster.ClusterAddress = util.CanonicalNetworkAddress(clusterAddress, ports.HTTPSDefaultPort)

			// Cluster certificate
			cert, err := localtls.GetRemoteCertificate(fmt.Sprintf("https://%s", config.Cluster.ClusterAddress), version.UserAgent)
			if err != nil {
				fmt.Printf("Error connecting to existing cluster member %q: %v\n", clusterAddress, err)
				continue
			}

			certDigest := localtls.CertFingerprint(cert)
			if joinToken.Fingerprint != certDigest {
				return fmt.Errorf("Certificate fingerprint mismatch between join token and cluster member %q", clusterAddress)
			}

			config.Cluster.ClusterCertificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))

			break // We've found a working cluster member.
		}

		if config.Cluster.ClusterCertificate == "" {
			return fmt.Errorf("Unable to connect to any of the cluster members specified in join token")
		}
	}

	// If clustering is enabled, and no cluster.https_address network address
	// was specified, we fallback to core.https_address.
	if config.Cluster != nil &&
		config.Node.Config["core.https_address"] != "" &&
		config.Node.Config["cluster.https_address"] == "" {
		config.Node.Config["cluster.https_address"] = config.Node.Config["core.https_address"]
	}

	// Detect if the user has chosen to join a cluster using the new
	// cluster join API format, and use the dedicated API if so.
	if config.Cluster != nil && config.Cluster.ClusterAddress != "" && config.Cluster.ServerAddress != "" {
		// Ensure the server and cluster addresses are in canonical form.
		config.Cluster.ServerAddress = util.CanonicalNetworkAddress(config.Cluster.ServerAddress, ports.HTTPSDefaultPort)
		config.Cluster.ClusterAddress = util.CanonicalNetworkAddress(config.Cluster.ClusterAddress, ports.HTTPSDefaultPort)

		op, err := d.UpdateCluster(config.Cluster.ClusterPut, "")
		if err != nil {
			return fmt.Errorf("Failed to join cluster: %w", err)
		}

		err = op.Wait()
		if err != nil {
			return fmt.Errorf("Failed to join cluster: %w", err)
		}

		return nil
	}

	revert := revert.New()
	defer revert.Fail()

	localRevert, err := initDataNodeApply(d, config.Node)
	if err != nil {
		return err
	}

	revert.Add(localRevert)

	err = initDataClusterApply(d, config.Cluster)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

func (c *cmdInit) defaultHostname() string {
	if c.hostname != "" {
		return c.hostname
	}

	// Cluster server name
	hostName, err := os.Hostname()
	if err != nil {
		hostName = "incus"
	}

	c.hostname = hostName
	return hostName
}
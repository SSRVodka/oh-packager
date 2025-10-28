package main

import (
	"fmt"
	"os"

	"github.com/SSRVodka/oh-packager/internal/common"
	"github.com/SSRVodka/oh-packager/internal/pkgclient"
	"github.com/spf13/cobra"
)

func main() {
	var rootURL, arch, channel, ohosSdkDir, ohosSdkDirAbs string
	root := &cobra.Command{
		Use:   "pkgclient",
		Short: "Client for the package repo (list, install, uninstall, config)",
	}

	// CONFIG
	cfgCmd := &cobra.Command{
		Use:   "config",
		Short: "Configure client",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := common.DefaultConfigPath()
			c, err := common.LoadConfig(cfg)
			if err != nil {
				c = common.DefaultConfig()
			}
			if rootURL == "" {
				return fmt.Errorf("the server root URL is required")
			}
			if !common.IsValidHttpUrl(rootURL) {
				return fmt.Errorf("invalid http URL: '%s'", rootURL)
			}
			c.RootURL = rootURL
			if ohosSdkDir == "" {
				return fmt.Errorf("the path for OHOS SDK is required")
			}
			var absErr error
			if ohosSdkDirAbs, absErr = common.GetAbsolutePath(ohosSdkDir); absErr != nil {
				return fmt.Errorf("invalid path '%s'", ohosSdkDir)
			}
			if !common.IsDirExists(ohosSdkDirAbs) {
				return fmt.Errorf("the directory '%s' doesn't exists", ohosSdkDirAbs)
			}
			c.OhosSdk = ohosSdkDirAbs
			if arch == "" {
				return fmt.Errorf("the default architecture is required")
			}
			c.Arch = arch
			if channel != "" {
				c.Channel = channel
			}
			if err := common.SaveConfig(cfg, c); err != nil {
				return err
			}
			fmt.Println("config saved to", cfg)
			return nil
		},
	}

	cfgCmd.Flags().StringVarP(&rootURL, "server-root", "s", "", "Set repository root URL (e.g. https://repo.example.com)")
	cfgCmd.Flags().StringVarP(&ohosSdkDir, "ohos-sdk", "d", "", "Set directory of local OHOS SDK (e.g. /home/xhw/ohos-robot-toolchain/linux)")
	cfgCmd.Flags().StringVarP(&arch, "arch", "a", "", "Set default architecture (e.g. x86_64,arm,aarch64)")
	cfgCmd.Flags().StringVarP(&channel, "channel", "c", "", "Set default channel (OPTIONAL, e.g. stable)")

	// LIST
	var archFlag string
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List packages available for current arch",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgFile := common.DefaultConfigPath()
			cfg, err := common.LoadConfig(cfgFile)
			if err != nil {
				return err
			}
			cl := pkgclient.NewClient(cfg)
			arch := archFlag
			if arch == "" {
				arch = common.DefaultArch()
			}
			return cl.ListPackages(arch)
		},
	}
	listCmd.Flags().StringVar(&archFlag, "arch", "", "architecture (default auto-detected)")

	// INSTALL
	var prefix string
	installCmd := &cobra.Command{
		Use:   "install <package>",
		Short: "Install a package",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgFile := common.DefaultConfigPath()
			cfg, err := common.LoadConfig(cfgFile)
			if err != nil {
				return err
			}
			cl := pkgclient.NewClient(cfg)
			pkg := args[0]
			if prefix == "" {
				return fmt.Errorf("--prefix required")
			}
			var prefixErr error
			prefix, prefixErr = common.GetAbsolutePath(prefix)
			if prefixErr != nil {
				return prefixErr
			}
			return cl.Install(pkg, prefix)
		},
	}
	installCmd.Flags().StringVar(&prefix, "prefix", "", "target install prefix (required)")

	// INSTALL to SDK
	installToSdkCmd := &cobra.Command{
		Use:   "add2sdk <package>",
		Short: "Install a package to OHOS sdk (irreversible)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgFile := common.DefaultConfigPath()
			cfg, err := common.LoadConfig(cfgFile)
			if err != nil {
				return err
			}
			cl := pkgclient.NewClient(cfg)
			pkg := args[0]
			return cl.InstallToSdk(pkg)
		},
	}

	// UNINSTALL
	uninstallCmd := &cobra.Command{
		Use:   "uninstall <package>",
		Short: "Uninstall a package from prefix (remove symlink and version dir)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgFile := common.DefaultConfigPath()
			cfg, err := common.LoadConfig(cfgFile)
			if err != nil {
				return err
			}
			cl := pkgclient.NewClient(cfg)
			pkg := args[0]
			if prefix == "" {
				return fmt.Errorf("--prefix required")
			}
			var prefixErr error
			prefix, prefixErr = common.GetAbsolutePath(prefix)
			if prefixErr != nil {
				return prefixErr
			}
			return cl.Uninstall(pkg, prefix)
		},
	}
	uninstallCmd.Flags().StringVar(&prefix, "prefix", "", "target install prefix (required)")

	root.AddCommand(cfgCmd, listCmd, installCmd, installToSdkCmd, uninstallCmd)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

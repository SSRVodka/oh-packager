package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/SSRVodka/oh-packager/internal/common"
	"github.com/SSRVodka/oh-packager/internal/pkgclient"
	"github.com/spf13/cobra"
)

func main() {
	var rootURL, arch, channel, ohosSdkDir, ohosSdkDirAbs string
	root := &cobra.Command{
		Use:   "oh-pkgmgr",
		Short: "Client for the package repo (list, install, uninstall, config)",
	}

	// CONFIG
	cfgCmd := &cobra.Command{
		Use:   "config",
		Short: "Configure client with repo URL, OHOS SDK path, etc.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := common.DefaultConfigPath()
			c, err := common.LoadConfig(cfg)
			if err != nil {
				c = common.DefaultConfig()
			}
			if rootURL == "" {
				return fmt.Errorf("the repo root URL is required")
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
				fmt.Printf("failed to load client config: %+v\n", err)
				return nil
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

	var tgtPrefix, newPrefix string
	patchCmd := &cobra.Command{
		Use:   "patch <prefix> <new_prefix>",
		Short: "Patch target libraries with new prefix",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgFile := common.DefaultConfigPath()
			cfg, err := common.LoadConfig(cfgFile)
			if err != nil {
				fmt.Printf("failed to load client config: %+v\n", err)
				return nil
			}
			cl := pkgclient.NewClient(cfg)
			tgtPrefix = args[0]
			newPrefix = args[1]

			// do patching
			tgtPrefix, err = filepath.Abs(tgtPrefix)
			if err != nil {
				return err
			}
			if !common.IsDirExists(tgtPrefix) {
				return fmt.Errorf("specific prefix not found: %s", tgtPrefix)
			}
			archDepRelPath, archErr := common.GetOhosArchDepLibDirRelPath(cfg.Arch)
			if archErr != nil {
				return fmt.Errorf("archError when patching (%v). Please reconfigure your arch using 'config'", archErr)
			}
			tgtArchLibDir := filepath.Join(tgtPrefix, archDepRelPath)
			newArchLibDir := filepath.Join(newPrefix, archDepRelPath)
			tgtShareDir := filepath.Join(tgtPrefix, common.GetOhosSharedDirRelPath())
			newShareDir := filepath.Join(newPrefix, common.GetOhosSharedDirRelPath())
			tgtArchIndepLibDir := filepath.Join(tgtPrefix, common.GetOhosArchIndepLibDirRelPath())
			newArchIndepLibDir := filepath.Join(newPrefix, common.GetOhosArchIndepLibDirRelPath())
			// patch arch-dep libs
			err = cl.PatchLibFiles(tgtArchLibDir, newArchLibDir, newPrefix)
			if err != nil {
				return fmt.Errorf("error while patching arch-dep libs: %v", err)
			}
			// patch shares libs
			err = cl.PatchLibFiles(tgtShareDir, newShareDir, newPrefix)
			if err != nil {
				return fmt.Errorf("error while shared arch-dep libs: %v", err)
			}
			// patch irregular arch-dep libs
			err = cl.PatchLibFiles(tgtArchIndepLibDir, newArchIndepLibDir, newPrefix)
			if err != nil {
				return fmt.Errorf("error while patching irregular arch-dep libs: %v", err)
			}
			return nil
		},
	}

	// INSTALL
	var prefix string
	var noConfirm bool
	installCmd := &cobra.Command{
		Use:   "add <package> [package...]",
		Short: "Install one or more packages to prefix (irreversible). Empty prefix indicates installing to OHOS sdk",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgFile := common.DefaultConfigPath()
			cfg, err := common.LoadConfig(cfgFile)
			if err != nil {
				fmt.Printf("failed to load client config: %+v\n", err)
				return nil
			}
			cl := pkgclient.NewClient(cfg)
			if prefix == "" {
				return cl.InstallToSdk(args, noConfirm)
			}
			var prefixErr error
			prefix, prefixErr = common.GetAbsolutePath(prefix)
			if prefixErr != nil {
				return prefixErr
			}
			return cl.Install(args, prefix, noConfirm)
		},
	}
	installCmd.Flags().BoolVarP(&noConfirm, "yes", "y", false, "install without interaction/prompt")
	installCmd.Flags().StringVar(&prefix, "prefix", "", "target install prefix (required for non OHOS sdk installation)")

	// UNINSTALL
	uninstallCmd := &cobra.Command{
		Use:   "del <package>",
		Short: "Uninstall a package from prefix (WARN: may break dependencies)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgFile := common.DefaultConfigPath()
			cfg, err := common.LoadConfig(cfgFile)
			if err != nil {
				fmt.Printf("failed to load client config: %+v\n", err)
				return nil
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

	// uninstall not supported for now
	// root.AddCommand(cfgCmd, listCmd, installCmd, uninstallCmd)
	root.AddCommand(cfgCmd, listCmd, installCmd, patchCmd)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

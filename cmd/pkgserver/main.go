package main

import (
	"fmt"
	"os"

	"github.com/SSRVodka/oh-packager/internal/common"
	"github.com/spf13/cobra"
)

func main() {
	var basePath string

	root := &cobra.Command{
		Use:   "oh-pkgserver",
		Short: "Repository manager for server-side package directory",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if basePath == "" {
				return fmt.Errorf("--repo is required")
			}
			return nil
		},
	}

	root.PersistentFlags().StringVar(&basePath, "repo", "", "path to repository root (required)")

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize repository directory structure",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := common.EnsureRepoDirs(basePath); err != nil {
				return err
			}
			fmt.Println("Repository structure created at", basePath)
			return nil
		},
	}

	var channel string
	deployCmd := &cobra.Command{
		Use:   "deploy <pkg-file> <manifest-file>",
		Short: "Deploy a .pkg and manifest to a channel and regenerate index.json",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pkgFile := args[0]
			manifestFile := args[1]
			if channel == "" {
				return fmt.Errorf("--channel is required")
			}
			if err := common.DeployPackage(basePath, channel, pkgFile, manifestFile); err != nil {
				return err
			}
			fmt.Printf("Deployed %s + %s to channel %s\n", pkgFile, manifestFile, channel)
			return nil
		},
	}
	deployCmd.Flags().StringVar(&channel, "channel", "stable", "channel to deploy to (default: stable)")

	root.AddCommand(initCmd, deployCmd)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

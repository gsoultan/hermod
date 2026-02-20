package main

import (
	"fmt"
	"github.com/user/hermod/internal/version"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of hermodctl",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("hermodctl %s\n", version.Version)
	},
}

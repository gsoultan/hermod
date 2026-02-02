package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of hermodctl",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("hermodctl v0.2.0 (Enterprise Edition)")
	},
}

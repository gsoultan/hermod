package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile string
	apiURL  string
	apiKey  string
)

var rootCmd = &cobra.Command{
	Use:   "hermodctl",
	Short: "hermodctl is a CLI for managing Hermod data platform",
	Long:  `A developer-focused terminal tool for linting workflows, managing secrets, and monitoring workers.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.hermodctl.yaml)")
	rootCmd.PersistentFlags().StringVar(&apiURL, "url", "http://localhost:8080", "Hermod API URL")
	rootCmd.PersistentFlags().StringVar(&apiKey, "key", "", "Hermod API Key")
	viper.BindPFlag("url", rootCmd.PersistentFlags().Lookup("url"))
	viper.BindPFlag("key", rootCmd.PersistentFlags().Lookup("key"))
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, _ := os.UserHomeDir()
		viper.AddConfigPath(home)
		viper.SetConfigType("yaml")
		viper.SetConfigName(".hermodctl")
	}

	viper.AutomaticEnv()
	if err := viper.ReadInConfig(); err == nil {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
	}
}

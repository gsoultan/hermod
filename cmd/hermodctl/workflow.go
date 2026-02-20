package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

func init() {
	rootCmd.AddCommand(workflowCmd)
	workflowCmd.AddCommand(lintCmd)
	workflowCmd.AddCommand(exportCmd)
	workflowCmd.AddCommand(importCmd)
	workflowCmd.AddCommand(testCmd)
}

var workflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Manage Hermod workflows",
}

var lintCmd = &cobra.Command{
	Use:   "lint [file]",
	Short: "Lint a workflow configuration file",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		data, err := os.ReadFile(args[0])
		if err != nil {
			fmt.Printf("Error reading file: %v\n", err)
			return
		}

		var wf struct {
			Nodes []any `json:"nodes"`
			Edges []any `json:"edges"`
		}

		if err := json.Unmarshal(data, &wf); err != nil {
			if err := yaml.Unmarshal(data, &wf); err != nil {
				fmt.Println("❌ Invalid JSON or YAML format")
				return
			}
		}

		fmt.Println("✅ Workflow format is valid")

		if len(wf.Nodes) == 0 {
			fmt.Println("⚠️  Warning: Workflow has no nodes")
		} else {
			fmt.Printf("📊 Workflow has %d nodes and %d edges\n", len(wf.Nodes), len(wf.Edges))
		}

		// Check for disconnected nodes (simple check)
		if len(wf.Nodes) > 1 && len(wf.Edges) == 0 {
			fmt.Println("⚠️  Warning: Multiple nodes present but no edges (disconnected graph)")
		}
	},
}

var exportCmd = &cobra.Command{
	Use:   "export [workflow-id]",
	Short: "Export a workflow configuration to YAML",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client := &http.Client{Timeout: 10 * time.Second}
		url := fmt.Sprintf("%s/api/workflows/%s", viper.GetString("url"), args[0])
		req, _ := http.NewRequest("GET", url, nil)
		if key := viper.GetString("key"); key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}

		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("Error connecting to API: %v\n", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Printf("API returned error: %s\n", resp.Status)
			return
		}

		var workflow any
		json.NewDecoder(resp.Body).Decode(&workflow)
		out, _ := yaml.Marshal(workflow)
		fmt.Println(string(out))
	},
}

var importCmd = &cobra.Command{
	Use:   "import [file]",
	Short: "Import a workflow configuration from YAML/JSON",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		data, err := os.ReadFile(args[0])
		if err != nil {
			fmt.Printf("Error reading file: %v\n", err)
			return
		}

		var workflow any
		if err := yaml.Unmarshal(data, &workflow); err != nil {
			fmt.Printf("Error parsing YAML: %v\n", err)
			return
		}

		jsonData, _ := json.Marshal(workflow)
		client := &http.Client{Timeout: 10 * time.Second}
		url := fmt.Sprintf("%s/api/workflows", viper.GetString("url"))
		req, _ := http.NewRequest("POST", url, bytes.NewReader(jsonData))
		req.Header.Set("Content-Type", "application/json")
		if key := viper.GetString("key"); key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}

		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("Error connecting to API: %v\n", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Printf("Import failed (%s): %s\n", resp.Status, string(body))
			return
		}

		fmt.Println("✅ Workflow imported successfully")
	},
}

var testCmd = &cobra.Command{
	Use:   "test [workflow-id] [payload-json]",
	Short: "Test a workflow with a sample payload (Dry Run)",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		workflowID := args[0]
		payload := "{}"
		if len(args) > 1 {
			payload = args[1]
		}

		fmt.Printf("🧪 Testing workflow %s with payload: %s\n", workflowID, payload)

		client := &http.Client{Timeout: 10 * time.Second}
		reqBody := bytes.NewBufferString(payload)
		url := fmt.Sprintf("%s/api/workflows/%s/test", viper.GetString("url"), workflowID)
		req, _ := http.NewRequest("POST", url, reqBody)
		req.Header.Set("Content-Type", "application/json")
		if key := viper.GetString("key"); key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}

		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			fmt.Printf("❌ Test failed (%s): %s\n", resp.Status, string(body))
			return
		}

		fmt.Printf("✅ Test result:\n%s\n", string(body))
	},
}

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var tailCmd = &cobra.Command{
	Use:   "tail",
	Short: "Tail system logs in real-time",
	Run: func(cmd *cobra.Command, args []string) {
		interrupt := make(chan os.Signal, 1)
		signal.Notify(interrupt, os.Interrupt)

		apiURL := viper.GetString("url")
		u, _ := url.Parse(apiURL)

		scheme := "ws"
		if u.Scheme == "https" {
			scheme = "wss"
		}

		wsURL := url.URL{Scheme: scheme, Host: u.Host, Path: "/api/ws/logs"}
		fmt.Printf("Connecting to %s...\n", wsURL.String())

		c, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
		if err != nil {
			log.Fatal("dial:", err)
		}
		defer c.Close()

		done := make(chan struct{})

		go func() {
			defer close(done)
			for {
				_, message, err := c.ReadMessage()
				if err != nil {
					log.Println("read:", err)
					return
				}

				var logEntry struct {
					Timestamp  time.Time `json:"timestamp"`
					Level      string    `json:"level"`
					Message    string    `json:"message"`
					SourceID   string    `json:"source_id,omitempty"`
					WorkflowID string    `json:"workflow_id,omitempty"`
				}

				if err := json.Unmarshal(message, &logEntry); err == nil {
					color := "\033[0m" // Default
					switch logEntry.Level {
					case "ERROR":
						color = "\033[31m" // Red
					case "WARN":
						color = "\033[33m" // Yellow
					case "INFO":
						color = "\033[32m" // Green
					}

					fmt.Printf("[%s] %s%s\033[0m %s",
						logEntry.Timestamp.Format("15:04:05"),
						color, logEntry.Level,
						logEntry.Message)

					if logEntry.WorkflowID != "" {
						fmt.Printf(" (wf: %s)", logEntry.WorkflowID)
					}
					fmt.Println()
				}
			}
		}()

		for {
			select {
			case <-done:
				return
			case <-interrupt:
				log.Println("interrupt")

				// Cleanly close the connection by sending a close message and then
				// waiting (with timeout) for the server to close the connection.
				err := c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				if err != nil {
					log.Println("write close:", err)
					return
				}
				select {
				case <-done:
				case <-time.After(time.Second):
				}
				return
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(tailCmd)
}

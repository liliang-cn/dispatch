package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/liliang-cn/dispatch/pkg/inventory"
	"github.com/liliang-cn/dispatch/pkg/executor"
	"github.com/liliang-cn/dispatch/pkg/logger"
	"github.com/spf13/cobra"
)

var (
	configPath string
	parallel   int
	timeout    int
	logLevel   string
	streamMode bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "dispatch",
		Short: "A simple SSH batch operation tool",
		Long: `dispatch - Execute commands on multiple servers via SSH

Examples:
  dispatch exec --hosts web -- "uptime"
  dispatch file send --src nginx.conf --dest /etc/nginx/nginx.conf --hosts web
  dispatch file get --src /var/log/app.log --dest ./logs/ --hosts web
  dispatch file update --src app.conf --dest /etc/app/app.conf --hosts web
  dispatch file delete --path /tmp/old.log --hosts web`,
	}

	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "", "Config file path (default: ~/.dispatch/config.toml)")
	rootCmd.PersistentFlags().IntVarP(&parallel, "parallel", "p", 0, "Parallel connections (default: 10)")
	rootCmd.PersistentFlags().IntVarP(&timeout, "timeout", "t", 0, "Timeout in seconds (default: 300)")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "", "Log level: debug, info, warn, error (default: info)")
	rootCmd.PersistentFlags().BoolVarP(&streamMode, "stream", "s", false, "Stream output in real-time")

	// Add subcommands
	rootCmd.AddCommand(execCmd())
	rootCmd.AddCommand(fileCmd())
	rootCmd.AddCommand(hostsCmd())
	rootCmd.AddCommand(configCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// getInventory gets inventory instance
func getInventory() (*inventory.Inventory, error) {
	return inventory.New(configPath)
}

// getExecutor gets executor instance
func getExecutor() (*executor.Executor, *inventory.Inventory, error) {
	inv, err := getInventory()
	if err != nil {
		return nil, nil, err
	}

	exec := executor.NewExecutor(inv)

	// If log level is specified via command line, override config
	if logLevel != "" {
		exec.SetLogger(logger.NewWithLevel(logLevel))
	}

	return exec, inv, nil
}

// execCmd executes command
func execCmd() *cobra.Command {
	var hosts []string
	var command string
	var input string

	cmd := &cobra.Command{
		Use:   "exec [OPTIONS] -- COMMAND",
		Short: "Execute command on multiple hosts",
		Example: `  dispatch exec --hosts web -- "uptime"
  dispatch exec --hosts "host1,host2" -p 5 -- "systemctl status nginx"
  dispatch exec --hosts web --stream -- "apt-get install nginx"  # Real-time output
  dispatch exec --hosts web --input "y\n" -- "script_needing_input.sh"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if command == "" {
				return fmt.Errorf("command is required")
			}
			if len(hosts) == 0 {
				return fmt.Errorf("--hosts is required")
			}

			exec, _, err := getExecutor()
			if err != nil {
				return fmt.Errorf("failed to load inventory: %w", err)
			}

			ctx := context.Background()
			req := &executor.ExecRequest{
				Hosts:    hosts,
				Cmd:      command,
				Parallel: parallel,
				Timeout:  time.Duration(timeout) * time.Second,
				Stream:   streamMode,
				Input:    input,
			}

			fmt.Printf("Executing on %v hosts...\n", hosts)
			fmt.Printf("Command: %s\n\n", command)

			return exec.Exec(ctx, req, func(result *executor.ExecResult) {
				duration := result.EndTime.Sub(result.StartTime).Milliseconds()

				if result.Err != nil {
					fmt.Printf("  [%s] Error: %v\n", result.Host, result.Err)
				} else if result.ExitCode != 0 {
					fmt.Printf("  [%s] Exit code: %d (took %dms)\n", result.Host, result.ExitCode, duration)
					if len(result.Error) > 0 {
						fmt.Printf("    stderr: %s\n", string(result.Error))
					}
				} else {
					fmt.Printf("  [%s] Success (took %dms)\n", result.Host, duration)
				}

				// Only print full output in non-streaming mode
				if !streamMode && len(result.Output) > 0 {
					fmt.Printf("    stdout: %s", string(result.Output))
				}
			})
		},
	}

	cmd.Flags().StringSliceVar(&hosts, "hosts", nil, "Host group or comma-separated host list (required)")
	cmd.Flags().StringVar(&command, "command", "", "Command to execute (use -- to separate flags)")
	cmd.Flags().StringVar(&input, "input", "", "Standard input string to pass to the command")

	return cmd
}

// fileCmd file operations command group
func fileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "file",
		Short: "File operations on remote hosts",
		Long:  `Batch file operations including send, get, update, and delete.`,
	}

	cmd.AddCommand(fileSendCmd())
	cmd.AddCommand(fileGetCmd())
	cmd.AddCommand(fileUpdateCmd())
	cmd.AddCommand(fileDeleteCmd())

	return cmd
}

// fileSendCmd sends file to remote hosts
func fileSendCmd() *cobra.Command {
	var hosts []string
	var src, dest string
	var mode int
	var backup bool

	cmd := &cobra.Command{
		Use:   "send [OPTIONS]",
		Short: "Send file to multiple hosts",
		Example: `  dispatch file send --src ./nginx.conf --dest /etc/nginx/nginx.conf --hosts web
  dispatch file send -s app.conf -d /etc/app/app.conf --hosts "host1,host2" --mode 644 --backup`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if src == "" {
				return fmt.Errorf("--src is required")
			}
			if dest == "" {
				return fmt.Errorf("--dest is required")
			}
			if len(hosts) == 0 {
				return fmt.Errorf("--hosts is required")
			}

			exec, _, err := getExecutor()
			if err != nil {
				return fmt.Errorf("failed to load inventory: %w", err)
			}


			ctx := context.Background()
			req := &executor.CopyRequest{
				Hosts:    hosts,
				Src:      src,
				Dest:     dest,
				Parallel: parallel,
				Mode:     mode,
				Backup:   backup,
			}

			fmt.Printf("Sending %s to %s on %v hosts...\n\n", src, dest, hosts)

			return exec.Copy(ctx, req, func(result *executor.CopyResult) {
				duration := result.EndTime.Sub(result.StartTime).Milliseconds()

				if result.Err != nil {
					fmt.Printf("  [%s] Error: %v\n", result.Host, result.Err)
				} else {
					fmt.Printf("  [%s] Sent %d bytes (took %dms)\n", result.Host, result.BytesCopied, duration)
				}
			})
		},
	}

	cmd.Flags().StringSliceVar(&hosts, "hosts", nil, "Host group or comma-separated host list (required)")
	cmd.Flags().StringVar(&src, "src", "", "Source file path (required)")
	cmd.Flags().StringVar(&dest, "dest", "", "Destination path on remote (required)")
	cmd.Flags().StringVar(&src, "s", "", "Short for src")
	cmd.Flags().StringVar(&dest, "d", "", "Short for dest")
	cmd.Flags().IntVar(&mode, "mode", 0644, "File permission (octal)")
	cmd.Flags().BoolVar(&backup, "backup", false, "Backup existing file as .bak")

	return cmd
}

// fileGetCmd gets file from remote hosts
func fileGetCmd() *cobra.Command {
	var hosts []string
	var src, dest string

	cmd := &cobra.Command{
		Use:   "get [OPTIONS]",
		Short: "Get file from multiple hosts",
		Example: `  dispatch file get --src /var/log/app.log --dest ./logs/ --hosts web
  dispatch file get -s /etc/nginx/nginx.conf -d ./configs/ --hosts "host1,host2"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if src == "" {
				return fmt.Errorf("--src is required")
			}
			if dest == "" {
				return fmt.Errorf("--dest is required")
			}
			if len(hosts) == 0 {
				return fmt.Errorf("--hosts is required")
			}

			exec, _, err := getExecutor()
			if err != nil {
				return fmt.Errorf("failed to load inventory: %w", err)
			}


			ctx := context.Background()
			req := &executor.FetchRequest{
				Hosts:    hosts,
				Src:      src,
				Dest:     dest,
				Parallel: parallel,
			}

			fmt.Printf("Getting %s from %v hosts to %s...\n\n", src, hosts, dest)

			return exec.Fetch(ctx, req, func(result *executor.FetchResult) {
				duration := result.EndTime.Sub(result.StartTime).Milliseconds()

				if result.Err != nil {
					fmt.Printf("  [%s] Error: %v\n", result.Host, result.Err)
				} else {
					fmt.Printf("  [%s] Got %s (%d bytes, took %dms)\n",
						result.Host, result.LocalPath, result.BytesFetched, duration)
				}
			})
		},
	}

	cmd.Flags().StringSliceVar(&hosts, "hosts", nil, "Host group or comma-separated host list (required)")
	cmd.Flags().StringVar(&src, "src", "", "Source path on remote (required)")
	cmd.Flags().StringVar(&dest, "dest", "", "Local destination directory (required)")
	cmd.Flags().StringVar(&src, "s", "", "Short for src")
	cmd.Flags().StringVar(&dest, "d", "", "Short for dest")

	return cmd
}

// fileUpdateCmd updates remote host files (only when changed)
func fileUpdateCmd() *cobra.Command {
	var hosts []string
	var src, dest string
	var mode int
	var backup bool

	cmd := &cobra.Command{
		Use:   "update [OPTIONS]",
		Short: "Update file on multiple hosts (only if changed)",
		Example: `  dispatch file update --src ./nginx.conf --dest /etc/nginx/nginx.conf --hosts web
  dispatch file update -s app.conf -d /etc/app/app.conf --hosts "host1,host2" --mode 644 --backup`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if src == "" {
				return fmt.Errorf("--src is required")
			}
			if dest == "" {
				return fmt.Errorf("--dest is required")
			}
			if len(hosts) == 0 {
				return fmt.Errorf("--hosts is required")
			}

			exec, _, err := getExecutor()
			if err != nil {
				return fmt.Errorf("failed to load inventory: %w", err)
			}


			ctx := context.Background()
			req := &executor.UpdateRequest{
				Hosts:    hosts,
				Src:      src,
				Dest:     dest,
				Parallel: parallel,
				Mode:     mode,
				Backup:   backup,
			}

			fmt.Printf("Updating %s to %s on %v hosts...\n\n", src, dest, hosts)

			return exec.Update(ctx, req, func(result *executor.UpdateResult) {
				duration := result.EndTime.Sub(result.StartTime).Milliseconds()

				if result.Err != nil {
					fmt.Printf("  [%s] Error: %v\n", result.Host, result.Err)
				} else if result.Skipped {
					fmt.Printf("  [%s] Skipped (unchanged, took %dms)\n", result.Host, duration)
				} else {
					fmt.Printf("  [%s] Updated %d bytes (took %dms)\n", result.Host, result.BytesCopied, duration)
				}
			})
		},
	}

	cmd.Flags().StringSliceVar(&hosts, "hosts", nil, "Host group or comma-separated host list (required)")
	cmd.Flags().StringVar(&src, "src", "", "Source file path (required)")
	cmd.Flags().StringVar(&dest, "dest", "", "Destination path on remote (required)")
	cmd.Flags().StringVar(&src, "s", "", "Short for src")
	cmd.Flags().StringVar(&dest, "d", "", "Short for dest")
	cmd.Flags().IntVar(&mode, "mode", 0644, "File permission (octal)")
	cmd.Flags().BoolVar(&backup, "backup", false, "Backup existing file as .bak")

	return cmd
}

// fileDeleteCmd deletes files on remote hosts
func fileDeleteCmd() *cobra.Command {
	var hosts []string
	var path string

	cmd := &cobra.Command{
		Use:   "delete [OPTIONS]",
		Short: "Delete file on multiple hosts",
		Example: `  dispatch file delete --path /tmp/old.log --hosts web
  dispatch file delete -p /var/log/app.log --hosts "host1,host2"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if path == "" {
				return fmt.Errorf("--path is required")
			}
			if len(hosts) == 0 {
				return fmt.Errorf("--hosts is required")
			}

			exec, _, err := getExecutor()
			if err != nil {
				return fmt.Errorf("failed to load inventory: %w", err)
			}


			ctx := context.Background()
			req := &executor.DeleteRequest{
				Hosts:    hosts,
				Path:     path,
				Parallel: parallel,
			}

			fmt.Printf("Deleting %s on %v hosts...\n\n", path, hosts)

			return exec.Delete(ctx, req, func(result *executor.DeleteResult) {
				duration := result.EndTime.Sub(result.StartTime).Milliseconds()

				if result.Err != nil {
					fmt.Printf("  [%s] Error: %v\n", result.Host, result.Err)
				} else {
					fmt.Printf("  [%s] Deleted (took %dms)\n", result.Host, duration)
				}
			})
		},
	}

	cmd.Flags().StringSliceVar(&hosts, "hosts", nil, "Host group or comma-separated host list (required)")
	cmd.Flags().StringVar(&path, "path", "", "Path on remote to delete (required)")
	cmd.Flags().StringVar(&path, "p", "", "Short for path")

	return cmd
}

// hostsCmd lists hosts
func hostsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hosts",
		Short: "List all hosts and groups",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, inv, err := getExecutor()
			if err != nil {
				return fmt.Errorf("failed to load inventory: %w", err)
			}

			groups := inv.GetAllGroups()

			fmt.Println("Host Groups:")
			fmt.Println()

			for name, hosts := range groups {
				fmt.Printf("  [%s]\n", name)
				for _, host := range hosts {
					fmt.Printf("    - %s\n", host)
				}
				fmt.Println()
			}

			config := inv.GetConfig()
			fmt.Printf("SSH Config:\n")
			fmt.Printf("  User: %s\n", config.SSH.User)
			fmt.Printf("  Port: %d\n", config.SSH.Port)
			fmt.Printf("  Key: %s\n", config.SSH.KeyPath)
			fmt.Printf("\n")
			fmt.Printf("Exec Config:\n")
			fmt.Printf("  Parallel: %d\n", config.Exec.Parallel)
			fmt.Printf("  Shell: %s\n", config.Exec.Shell)

			return nil
		},
	}

	return cmd
}

// configCmd opens configuration file
func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Open or create config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			var cfgPath string
			if configPath == "" {
				home, _ := os.UserHomeDir()
				cfgPath = os.ExpandEnv(filepath.Join(home, ".dispatch", "config.toml"))
			} else {
				cfgPath = configPath
			}

			// Ensure directory exists
			dir := filepath.Dir(cfgPath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("failed to create config directory: %w", err)
			}

			// If file doesn't exist, create example
			if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
				fmt.Printf("Creating new config at %s\n", cfgPath)
			}

			// Open with editor
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}

			fmt.Printf("Opening %s with %s...\n", cfgPath, editor)

			return nil
		},
	}

	return cmd
}

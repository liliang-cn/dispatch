package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/liliang-cn/dispatch/pkg/executor"
	"github.com/liliang-cn/dispatch/pkg/inventory"
	"github.com/liliang-cn/dispatch/pkg/logger"
	"github.com/liliang-cn/dispatch/pkg/tui"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var (
	Version = "dev" // Set at build time

	configPath string
	parallel   int
	timeout    int
	logLevel   string
	noTUI      bool // Disable TUI mode, use text output
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "dispatch",
		Short:   "A simple SSH batch operation tool",
		Version: Version,
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
	rootCmd.PersistentFlags().BoolVar(&noTUI, "no-tui", false, "Disable TUI mode, use text output")

	// Set version template
	rootCmd.SetVersionTemplate(`{{with .Name}}{{printf "%s " .}}{{end}}{{printf "version %s" .Version}}
`)

	// Add subcommands
	rootCmd.AddCommand(execCmd())
	rootCmd.AddCommand(fileCmd())
	rootCmd.AddCommand(hostsCmd())
	rootCmd.AddCommand(versionCmd())

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
  dispatch exec --hosts web --input "y\n" -- "script_needing_input.sh"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// If command is not set via flag, try to get it from args
			if command == "" && len(args) > 0 {
				command = args[0]
				// If there are more args, join them (though usually better to quote the command)
				if len(args) > 1 {
					// This handles cases like: dispatch exec ... -- ls -la
					// But users should prefer: dispatch exec ... -- "ls -la"
					for _, arg := range args[1:] {
						command += " " + arg
					}
				}
			}

			if command == "" {
				return fmt.Errorf("command is required")
			}
			if len(hosts) == 0 {
				return fmt.Errorf("--hosts is required")
			}

			exec, inv, err := getExecutor()
			if err != nil {
				return fmt.Errorf("failed to load inventory: %w", err)
			}

			// Expand hosts to get actual count
			expandedHosts, err := inv.GetHosts(hosts)
			if err != nil {
				return err
			}
			hostNames := make([]string, len(expandedHosts))
			for i, h := range expandedHosts {
				hostNames[i] = h.Address
			}

			ctx := context.Background()

			// Setup for TUI (only if we have a TTY and multiple hosts, or forced)
			var program *tea.Program
			var execModel *tui.ExecModel
			useTUI := !noTUI && len(hostNames) > 1 && isatty.IsTerminal(os.Stdout.Fd())

			if useTUI {
				model := tui.NewExecModel(hostNames)
				model.SetCommand(command)
				execModel = &model
				// Use bubbletea without altscreen for in-place updates
				program = tea.NewProgram(execModel, tea.WithoutSignalHandler())
			} else {
				fmt.Printf("Executing on %d hosts...\n", len(hostNames))
				fmt.Printf("Command: %s\n\n", command)
			}

			// 使用 streaming 模式获取实时输出
			req := &executor.ExecRequest{
				Hosts:    hosts,
				Cmd:      command,
				Parallel: parallel,
				Timeout:  time.Duration(timeout) * time.Second,
				Stream:   true, // 启用 streaming 实时输出
				Input:    input,
			}

			// 收集每个主机的输出状态
			type hostExecState struct {
				mu       sync.Mutex
				output   strings.Builder
				stderr   strings.Builder
				exitCode int
				err      error
				done     bool
			}
			hostStates := make(map[string]*hostExecState)
			for _, h := range hostNames {
				hostStates[h] = &hostExecState{}
			}

			// Set StreamCallback to capture streaming output
			if useTUI {
				req.StreamCallback = func(host, streamType string, data []byte) {
					hs := hostStates[host]
					if hs == nil {
						return
					}
					hs.mu.Lock()
					defer hs.mu.Unlock()

					if streamType == "stderr" {
						hs.stderr.Write(data)
					} else {
						hs.output.Write(data)
					}

					// Send update to TUI
					output := hs.output.String()
					if hs.stderr.Len() > 0 {
						output += "\n" + hs.stderr.String()
					}
					program.Send(tui.ExecStatusMsg{
						Host:   host,
						Status: "running",
						Output: output,
					})
				}
			}

			errChan := make(chan error, 1)
			go func() {
				err := exec.Exec(ctx, req, func(result *executor.ExecResult) {
					hs := hostStates[result.Host]
					hs.mu.Lock()
					defer hs.mu.Unlock()

					if useTUI {
						// Only handle final result (StreamCallback handles streaming)
						if !result.EndTime.IsZero() {
							status := "done"
							if result.Err != nil || result.ExitCode != 0 {
								status = "error"
							}

							// Get accumulated output from hostState
							output := hs.output.String()
							if hs.stderr.Len() > 0 {
								if output != "" {
									output += "\n"
								}
								output += hs.stderr.String()
							}

							program.Send(tui.ExecStatusMsg{
								Host:     result.Host,
								Status:   status,
								Output:   output,
								ExitCode: result.ExitCode,
								Err:      result.Err,
							})
						}
					} else {
						// CLI text mode
						// 打印 stderr（错误警告等）
						if len(result.Error) > 0 {
							newError := string(result.Error)
							existingErrLen := hs.output.Len()
							totalErrLen := len(newError)

							if totalErrLen > existingErrLen {
								newPart := newError[existingErrLen:]
								for _, line := range strings.Split(newPart, "\n") {
									if line != "" && !strings.Contains(hs.output.String(), line) {
										fmt.Printf("[%s] stderr: %s\n", result.Host, line)
									}
								}
							}
						}

						// 打印新的输出（实时滚动）
						if len(result.Output) > 0 {
							newOutput := string(result.Output)
							existingLen := hs.output.Len()
							totalLen := len(newOutput)

							if totalLen > existingLen {
								// 只打印新增的部分
								newPart := newOutput[existingLen:]
								hs.output.WriteString(newPart)
								// 逐行打印，跳过空行
								lines := strings.Split(newPart, "\n")
								for i, line := range lines {
									if line != "" || (i < len(lines)-1) {
										fmt.Printf("[%s] %s\n", result.Host, line)
									}
								}
							}
						}

						// 记录退出码和错误
						if result.ExitCode != 0 {
							hs.exitCode = result.ExitCode
						}
						if result.Err != nil {
							hs.err = result.Err
						}

						// 命令完成时显示最终状态
						if !result.EndTime.IsZero() {
							hs.done = true
							duration := result.EndTime.Sub(result.StartTime).Milliseconds()
							fmt.Printf("  [%s] ", result.Host)
							if hs.err != nil {
								fmt.Printf("\033[31m✗ Error: %v\033[0m (%dms)\n", hs.err, duration)
							} else if hs.exitCode != 0 {
								fmt.Printf("\033[31m✗ Exit code: %d\033[0m (%dms)\n", hs.exitCode, duration)
							} else {
								fmt.Printf("\033[32m✓ Success\033[0m (%dms)\n", duration)
							}
						}
					}
				})
				if useTUI {
					program.Send(tui.ExecDoneMsg{})
				}
				errChan <- err
			}()

			if useTUI {
				_, err := program.Run()
				if err != nil {
					return err
				}
				// No need to print again, table is already displayed
			}
			return <-errChan
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

			exec, inv, err := getExecutor()
			if err != nil {
				return fmt.Errorf("failed to load inventory: %w", err)
			}

			// Expand hosts to get actual count
			expandedHosts, err := inv.GetHosts(hosts)
			if err != nil {
				return err
			}
			hostNames := make([]string, len(expandedHosts))
			for i, h := range expandedHosts {
				hostNames[i] = h.Address
			}

			ctx := context.Background()

			// Setup for single host
			var mu sync.Mutex
			lastUpdate := make(map[string]time.Time)

			// Setup for TUI (only if we have a TTY)
			var program *tea.Program
			useTUI := !noTUI && len(hostNames) > 1 && isatty.IsTerminal(os.Stdout.Fd())
			if useTUI {
				model := tui.NewModel(hostNames)
				program = tea.NewProgram(model, tea.WithoutSignalHandler())
			}

			req := &executor.CopyRequest{
				Hosts:    hosts, // Executor will expand again, which is fine
				Src:      src,
				Dest:     dest,
				Parallel: parallel,
				Mode:     mode,
				Backup:   backup,
				ProgressCallback: func(info executor.ProgressInfo) {
					mu.Lock()
					defer mu.Unlock()

					// Rate limiting: Update max 10 times per second per host, unless finished
					now := time.Now()
					if now.Sub(lastUpdate[info.Host]) < 100*time.Millisecond && info.Current < info.Total {
						return
					}
					lastUpdate[info.Host] = now

					if useTUI {
						program.Send(tui.ProgressMsg{
							Host:    info.Host,
							Current: info.Current,
							Total:   info.Total,
						})
						return
					}

					// CLI text mode - show progress for all hosts
					if info.Total > 0 {
						percent := int(float64(info.Current) * 100 / float64(info.Total))
						fmt.Printf("\r  [%s] Uploading: %d%% (%d/%d bytes)\033[K",
							info.Host, percent, info.Current, info.Total)
					} else {
						fmt.Printf("\r  [%s] Uploading: %d bytes\033[K", info.Host, info.Current)
					}
				},
			}

			fmt.Printf("Sending %s to %s on %v hosts...\n\n", src, dest, hosts)

			// Run execution
			errChan := make(chan error, 1)
			go func() {
				err := exec.Copy(ctx, req, func(result *executor.CopyResult) {
					if useTUI {
						if result.Err != nil {
							program.Send(tui.ProgressMsg{Host: result.Host, Err: result.Err})
						}
						// Completion is handled by progress reaching 100% or error
					} else {
						// Clear entire line before printing result
						fmt.Print("\r\033[K") // \033[K clears rest of line
						duration := result.EndTime.Sub(result.StartTime).Milliseconds()
						if result.Err != nil {
							fmt.Printf("  [%s] Error: %v\n", result.Host, result.Err)
						} else {
							fmt.Printf("  [%s] Sent %d bytes (took %dms)\n", result.Host, result.BytesCopied, duration)
						}
					}
				})
				if useTUI {
					program.Send(tui.DoneMsg{})
				}
				errChan <- err
			}()

			if useTUI {
				if _, err := program.Run(); err != nil {
					return err
				}
			}

			return <-errChan
		},
	}

	cmd.Flags().StringSliceVar(&hosts, "hosts", nil, "Host group or comma-separated host list (required)")
	cmd.Flags().StringVarP(&src, "src", "s", "", "Source file path (required)")
	cmd.Flags().StringVarP(&dest, "dest", "d", "", "Destination path on remote (required)")
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

			exec, inv, err := getExecutor()
			if err != nil {
				return fmt.Errorf("failed to load inventory: %w", err)
			}

			// Expand hosts
			expandedHosts, err := inv.GetHosts(hosts)
			if err != nil {
				return err
			}
			hostNames := make([]string, len(expandedHosts))
			for i, h := range expandedHosts {
				hostNames[i] = h.Address
			}

			ctx := context.Background()

			var mu sync.Mutex
			lastUpdate := make(map[string]time.Time)

			// Setup for TUI (only if we have a TTY)
			var program *tea.Program
			useTUI := !noTUI && len(hostNames) > 1 && isatty.IsTerminal(os.Stdout.Fd())
			if useTUI {
				model := tui.NewModel(hostNames)
				program = tea.NewProgram(model, tea.WithoutSignalHandler())
			}

			req := &executor.FetchRequest{
				Hosts:    hosts,
				Src:      src,
				Dest:     dest,
				Parallel: parallel,
				ProgressCallback: func(info executor.ProgressInfo) {
					mu.Lock()
					defer mu.Unlock()

					now := time.Now()
					if now.Sub(lastUpdate[info.Host]) < 100*time.Millisecond && info.Current < info.Total {
						return
					}
					lastUpdate[info.Host] = now

					if useTUI {
						program.Send(tui.ProgressMsg{
							Host:    info.Host,
							Current: info.Current,
							Total:   info.Total,
						})
						return
					}

					// CLI text mode - show progress for all hosts
					if info.Total > 0 {
						percent := int(float64(info.Current) * 100 / float64(info.Total))
						fmt.Printf("\r  [%s] Downloading: %d%% (%d/%d bytes)\033[K",
							info.Host, percent, info.Current, info.Total)
					} else {
						fmt.Printf("\r  [%s] Downloading: %d bytes\033[K", info.Host, info.Current)
					}
				},
			}

			fmt.Printf("Getting %s from %v hosts to %s...\n\n", src, hosts, dest)

			errChan := make(chan error, 1)
			go func() {
				err := exec.Fetch(ctx, req, func(result *executor.FetchResult) {
					if useTUI {
						if result.Err != nil {
							program.Send(tui.ProgressMsg{Host: result.Host, Err: result.Err})
						}
					} else {
						// Clear entire line before printing result
						fmt.Print("\r\033[K")
						duration := result.EndTime.Sub(result.StartTime).Milliseconds()
						if result.Err != nil {
							fmt.Printf("  [%s] Error: %v\n", result.Host, result.Err)
						} else {
							fmt.Printf("  [%s] Got %s (%d bytes, took %dms)\n",
								result.Host, result.LocalPath, result.BytesFetched, duration)
						}
					}
				})
				if useTUI {
					program.Send(tui.DoneMsg{})
				}
				errChan <- err
			}()

			if useTUI {
				if _, err := program.Run(); err != nil {
					return err
				}
			}

			return <-errChan
		},
	}

	cmd.Flags().StringSliceVar(&hosts, "hosts", nil, "Host group or comma-separated host list (required)")
	cmd.Flags().StringVarP(&src, "src", "s", "", "Source path on remote (required)")
	cmd.Flags().StringVarP(&dest, "dest", "d", "", "Local destination directory (required)")

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

			exec, inv, err := getExecutor()
			if err != nil {
				return fmt.Errorf("failed to load inventory: %w", err)
			}

			// Expand hosts
			expandedHosts, err := inv.GetHosts(hosts)
			if err != nil {
				return err
			}
			hostNames := make([]string, len(expandedHosts))
			for i, h := range expandedHosts {
				hostNames[i] = h.Address
			}

			ctx := context.Background()

			var mu sync.Mutex
			lastUpdate := make(map[string]time.Time)

			// Setup for TUI (only if we have a TTY)
			var program *tea.Program
			useTUI := !noTUI && len(hostNames) > 1 && isatty.IsTerminal(os.Stdout.Fd())
			if useTUI {
				model := tui.NewModel(hostNames)
				program = tea.NewProgram(model, tea.WithoutSignalHandler())
			}

			req := &executor.UpdateRequest{
				Hosts:    hosts,
				Src:      src,
				Dest:     dest,
				Parallel: parallel,
				Mode:     mode,
				Backup:   backup,
				ProgressCallback: func(info executor.ProgressInfo) {
					mu.Lock()
					defer mu.Unlock()

					now := time.Now()
					if now.Sub(lastUpdate[info.Host]) < 100*time.Millisecond && info.Current < info.Total {
						return
					}
					lastUpdate[info.Host] = now

					if useTUI {
						program.Send(tui.ProgressMsg{
							Host:    info.Host,
							Current: info.Current,
							Total:   info.Total,
						})
						return
					}

					// CLI text mode - show progress for all hosts
					if info.Total > 0 {
						percent := int(float64(info.Current) * 100 / float64(info.Total))
						fmt.Printf("\r  [%s] Updating: %d%% (%d/%d bytes)\033[K",
							info.Host, percent, info.Current, info.Total)
					} else {
						fmt.Printf("\r  [%s] Updating: %d bytes\033[K", info.Host, info.Current)
					}
				},
			}

			fmt.Printf("Updating %s to %s on %v hosts...\n\n", src, dest, hosts)

			errChan := make(chan error, 1)
			go func() {
				err := exec.Update(ctx, req, func(result *executor.UpdateResult) {
					if useTUI {
						if result.Err != nil {
							program.Send(tui.ProgressMsg{Host: result.Host, Err: result.Err})
						} else if result.Skipped {
							// For update, skipped means done (100%) but maybe we want to show "Skipped" status?
							// For now, let's just mark it done.
							// Ideally tui should support "Skipped" status.
							// We can send Current=Total to mark done.
							// Or use error to pass a message? No.
							// Let's just mark as done.
							program.Send(tui.ProgressMsg{
								Host:    result.Host,
								Current: 100,
								Total:   100,
							})
						}
					} else {
						// Clear entire line before printing result
						fmt.Print("\r\033[K")
						duration := result.EndTime.Sub(result.StartTime).Milliseconds()
						if result.Err != nil {
							fmt.Printf("  [%s] Error: %v\n", result.Host, result.Err)
						} else if result.Skipped {
							fmt.Printf("  [%s] Skipped (unchanged, took %dms)\n", result.Host, duration)
						} else {
							fmt.Printf("  [%s] Updated %d bytes (took %dms)\n", result.Host, result.BytesCopied, duration)
						}
					}
				})
				if useTUI {
					program.Send(tui.DoneMsg{})
				}
				errChan <- err
			}()

			if useTUI {
				if _, err := program.Run(); err != nil {
					return err
				}
			}

			return <-errChan
		},
	}

	cmd.Flags().StringSliceVar(&hosts, "hosts", nil, "Host group or comma-separated host list (required)")
	cmd.Flags().StringVarP(&src, "src", "s", "", "Source file path (required)")
	cmd.Flags().StringVarP(&dest, "dest", "d", "", "Destination path on remote (required)")
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

// versionCmd returns version command
func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version number of dispatch",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("dispatch version %s\n", Version)
		},
	}
}

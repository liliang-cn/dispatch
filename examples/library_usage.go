package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/liliang-cn/dispatch/pkg/dispatch"
)

func main() {
	// 示例 1: 使用默认配置
	// 会读取 ~/.dispatch/config.toml
	client, err := dispatch.New(nil)
	if err != nil {
		log.Fatal(err)
	}

	// 示例 2: 使用自定义配置
	/*
	client, err := dispatch.New(&dispatch.Config{
		ConfigPath: "/path/to/config.toml",
		SSH: &dispatch.SSHConfig{
			User:    "root",
			Port:    22,
			KeyPath: "~/.ssh/id_rsa",
			Timeout: 30,
		},
		Exec: &dispatch.ExecConfig{
			Parallel: 10,
			Timeout:  300,
			Shell:    "/bin/bash",
		},
	})
	*/

	ctx := context.Background()

	// ========== 示例 1: 批量执行命令 ==========
	fmt.Println("=== 示例 1: 批量执行命令 ===")

	result, err := client.Exec(ctx, []string{"web"}, "uptime",
		dispatch.WithTimeout(30*time.Second),
		dispatch.WithParallel(5),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 遍历结果
	for host, r := range result.Hosts {
		if r.Success {
			fmt.Printf("[%s] ✅ %s\n", host, r.Output)
		} else {
			fmt.Printf("[%s] ❌ Exit: %d, Error: %s\n", host, r.ExitCode, r.Error)
		}
	}

	// 检查是否全部成功
	if result.AllSuccess() {
		fmt.Println("所有主机执行成功!")
	} else {
		fmt.Printf("失败的主机: %v\n", result.FailedHosts())
	}

	// ========== 示例 2: 批量复制文件 ==========
	fmt.Println("\n=== 示例 2: 批量复制文件 ===")

	copyResult, err := client.Copy(ctx, []string{"web"}, "./app.conf", "/etc/app/app.conf",
		dispatch.WithCopyMode(0644),
	)
	if err != nil {
		log.Fatal(err)
	}

	for host, r := range copyResult.Hosts {
		if r.Success {
			fmt.Printf("[%s] ✅ 复制了 %d 字节\n", host, r.BytesCopied)
		} else {
			fmt.Printf("[%s] ❌ %v\n", host, r.Error)
		}
	}

	// ========== 示例 3: 批量下载文件 ==========
	fmt.Println("\n=== 示例 3: 批量下载文件 ===")

	fetchResult, err := client.Fetch(ctx, []string{"web"}, "/var/log/app.log", "./logs/")
	if err != nil {
		log.Fatal(err)
	}

	for host, r := range fetchResult.Hosts {
		if r.Success {
			fmt.Printf("[%s] ✅ 下载到 %s (%d 字节)\n", host, r.LocalPath, r.BytesFetched)
		} else {
			fmt.Printf("[%s] ❌ %v\n", host, r.Error)
		}
	}

	// ========== 示例 4: 带环境变量的命令 ==========
	fmt.Println("\n=== 示例 4: 带环境变量的命令 ===")

	result, err = client.Exec(ctx, []string{"web"}, "echo $APP_ENV",
		dispatch.WithEnv(map[string]string{
			"APP_ENV": "production",
		}),
	)

	// ========== 示例 5: 在指定目录执行 ==========
	fmt.Println("\n=== 示例 5: 在指定目录执行 ===")

	result, err = client.Exec(ctx, []string{"web"}, "pwd",
		dispatch.WithDir("/app"),
	)

	// ========== 示例 6: 直接指定主机地址 ==========
	fmt.Println("\n=== 示例 6: 直接指定主机地址 ===")

	result, err = client.Exec(ctx, []string{"192.168.1.10", "192.168.1.11"}, "hostname")

	// ========== 示例 7: 获取主机信息 ==========
	fmt.Println("\n=== 示例 7: 获取主机信息 ===")

	hosts, err := client.GetHosts([]string{"web"})
	if err != nil {
		log.Fatal(err)
	}

	for _, host := range hosts {
		fmt.Printf("- %s@%s:%d\n", host.User, host.Address, host.Port)
	}

	// 获取所有组
	groups := client.GetAllGroups()
	for name, addrs := range groups {
		fmt.Printf("组[%s]: %v\n", name, addrs)
	}

	// ========== 示例 8: 流式处理结果 ==========
	fmt.Println("\n=== 示例 8: 流式处理结果 ===")

	// 获取 inventory 直接使用 executor
	inv := client.GetInventory()

	// 可以直接访问更底层的功能
	config := inv.GetConfig()
	fmt.Printf("默认并发数: %d\n", config.Exec.Parallel)
	fmt.Printf("默认超时: %s\n", config.Exec.Timeout)
}

// ========== 高级用法示例 ==========

// 示例: 部署应用
func DeployApp(client *dispatch.Dispatch, hosts []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// 1. 停止服务
	fmt.Println("1. 停止服务...")
	result, err := client.Exec(ctx, hosts, "systemctl stop myapp")
	if err != nil {
		return err
	}
	if !result.AllSuccess() {
		return fmt.Errorf("停止服务失败: %v", result.FailedHosts())
	}

	// 2. 复制新文件
	fmt.Println("2. 部署新文件...")
	copyResult, err := client.Copy(ctx, hosts, "./myapp", "/usr/local/bin/myapp",
		dispatch.WithCopyMode(0755),
	)
	if err != nil {
		return err
	}
	if len(copyResult.Hosts) != len(hosts) {
		return fmt.Errorf("文件复制失败")
	}

	// 3. 启动服务
	fmt.Println("3. 启动服务...")
	result, err = client.Exec(ctx, hosts, "systemctl start myapp")
	if err != nil {
		return err
	}
	if !result.AllSuccess() {
		return fmt.Errorf("启动服务失败: %v", result.FailedHosts())
	}

	// 4. 检查状态
	fmt.Println("4. 检查服务状态...")
	result, err = client.Exec(ctx, hosts, "systemctl status myapp")
	if err != nil {
		return err
	}

	for host, r := range result.Hosts {
		if r.Success {
			fmt.Printf("[%s] %s\n", host, r.Output)
		}
	}

	return nil
}

// 示例: 滚动更新
func RollingUpdate(client *dispatch.Dispatch, hosts []string) error {
	ctx := context.Background()

	// 每次只更新一台机器
	for _, host := range hosts {
		fmt.Printf("更新 %s...\n", host)

		result, err := client.Exec(ctx, []string{host}, "systemctl restart myapp",
			dispatch.WithTimeout(60*time.Second),
		)
		if err != nil {
			return err
		}

		if r, ok := result.Hosts[host]; ok {
			if r.Success {
				fmt.Printf("[%s] ✅ 更新成功\n", host)
			} else {
				fmt.Printf("[%s] ❌ 更新失败: %s\n", host, r.Error)
				// 继续更新下一台
			}
		}

		// 等待一下再更新下一台
		time.Sleep(5 * time.Second)
	}

	return nil
}

package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "numscan",
	Short: "CLI 工具：讀取 CSV 並透過 FreeSWITCH 撥打電話",
	Long:  "這是一個用於讀取 CSV 文件並通過 FreeSWITCH 撥打電話的命令行工具。",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

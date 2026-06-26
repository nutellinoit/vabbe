package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var imageTag string

var imageCmd = &cobra.Command{
	Use:   "image",
	Short: "Node image helpers",
}

var imageBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build the bundled VM base image (host arch) via the Docker Engine API",
	RunE: func(cmd *cobra.Command, args []string) error {
		dk, err := NewDocker()
		if err != nil {
			return err
		}
		tags := []string{imageTag}
		fmt.Printf("building %s ...\n", imageTag)
		if err := dk.BuildImage(context.Background(), adminDockerfile, adminBootIDUnit, tags, os.Stdout); err != nil {
			return err
		}
		fmt.Printf("done: %s\n", imageTag)
		return nil
	},
}

func init() {
	imageBuildCmd.Flags().StringVar(&imageTag, "tag", DefaultImage, "image tag to build")
	imageCmd.AddCommand(imageBuildCmd)
	rootCmd.AddCommand(imageCmd)
}

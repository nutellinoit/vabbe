package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

var (
	imageTag  string
	imageBase string
)

var imageCmd = &cobra.Command{
	Use:   "image",
	Short: "Node image helpers",
}

var imageBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build a bundled VM base image (host arch) via the Docker Engine API",
	RunE: func(_ *cobra.Command, _ []string) error {
		if !slices.Contains(imageBases, imageBase) {
			return fmt.Errorf("unknown --base %q; choose one of: %s", imageBase, strings.Join(imageBases, ", "))
		}
		dockerfile, err := baseDockerfile(imageBase)
		if err != nil {
			return fmt.Errorf("base %q: %w", imageBase, err)
		}
		unit, err := bootIDUnit()
		if err != nil {
			return err
		}
		dk, err := NewDocker()
		if err != nil {
			return err
		}
		tags := []string{imageTag}
		fmt.Printf("building %s (base: %s) ...\n", imageTag, imageBase)
		if err := dk.BuildImage(context.Background(), dockerfile, unit, tags, os.Stdout); err != nil {
			return err
		}
		fmt.Printf("done: %s\n", imageTag)
		return nil
	},
}

func init() {
	imageBuildCmd.Flags().StringVar(&imageTag, "tag", DefaultImage, "image tag to build")
	imageBuildCmd.Flags().StringVar(&imageBase, "base", "ubuntu",
		"node base to build: "+strings.Join(imageBases, ", "))
	imageCmd.AddCommand(imageBuildCmd)
	rootCmd.AddCommand(imageCmd)
}

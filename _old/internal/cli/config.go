package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect the effective sandbox configuration",
	}
	cmd.AddCommand(
		newConfigShowCmd(),
		newConfigPathCmd(),
		newConfigValidateCmd(),
	)
	return cmd
}

func loadEffective() (config.Config, error) {
	wd, _ := os.Getwd()
	cfg, err := config.Load(wd, "")
	if err != nil {
		return cfg, err
	}
	return cfg, cfg.Validate()
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the merged configuration as YAML",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadEffective()
			if err != nil {
				return err
			}
			out, err := yaml.Marshal(cfg)
			if err != nil {
				return err
			}
			fmt.Print(string(out))
			return nil
		},
	}
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the config files that would be loaded",
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, _ := os.Getwd()
			user := config.UserConfigPath()
			proj := config.FindProjectConfig(wd)
			fmt.Printf("user:    %s\n", pathStatus(user))
			fmt.Printf("project: %s\n", pathStatus(proj))
			return nil
		},
	}
}

func pathStatus(p string) string {
	if p == "" {
		return "(none)"
	}
	if _, err := os.Stat(p); err != nil {
		return p + " (not present)"
	}
	return p
}

func newConfigValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate the merged configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := loadEffective(); err != nil {
				return err
			}
			fmt.Println("config OK")
			return nil
		},
	}
}

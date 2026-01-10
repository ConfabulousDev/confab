package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/spf13/cobra"
)

var installDest string

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install confab binary to your PATH",
	Long: `Copies the confab binary to ~/.local/bin/ (or a custom location).

If the destination directory is not in your PATH, provides shell-specific
instructions to add it.

This command is typically invoked by the official installation script.`,
	RunE: runInstall,
}

func runInstall(cmd *cobra.Command, args []string) error {
	logger.Info("Running install command")

	fmt.Println("=== Confab: Install ===")
	fmt.Println()

	// Get current executable path
	execPath, err := os.Executable()
	if err != nil {
		logger.Error("Failed to get executable path: %v", err)
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Resolve symlinks to get the real path
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		logger.Error("Failed to resolve executable path: %v", err)
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}
	logger.Debug("Current executable: %s", execPath)

	// Determine destination directory
	destDir := installDest
	if destDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			logger.Error("Failed to get home directory: %v", err)
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		destDir = filepath.Join(homeDir, ".local", "bin")
	}

	// Expand ~ if present
	if strings.HasPrefix(destDir, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			logger.Error("Failed to get home directory: %v", err)
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		destDir = filepath.Join(homeDir, destDir[2:])
	}

	destPath := filepath.Join(destDir, "confab")
	logger.Info("Installing to %s", destPath)

	// Check if source and destination are the same
	srcAbs, _ := filepath.Abs(execPath)
	destAbs, _ := filepath.Abs(destPath)
	if srcAbs == destAbs {
		fmt.Printf("Confab is already installed at %s\n", destPath)
		fmt.Println()
	} else {
		// Create destination directory if it doesn't exist
		fmt.Printf("Installing confab to %s...\n", destPath)
		if err := os.MkdirAll(destDir, 0755); err != nil {
			logger.Error("Failed to create directory %s: %v", destDir, err)
			return fmt.Errorf("failed to create directory %s: %w", destDir, err)
		}

		// Remove existing binary first to get a fresh inode.
		// On macOS, the kernel caches code signing info per-inode, and overwriting
		// a file reuses the inode, causing signature verification to fail.
		// See: https://developer.apple.com/forums/thread/669145
		if err := os.Remove(destPath); err != nil && !os.IsNotExist(err) {
			logger.Error("Failed to remove existing binary: %v", err)
			return fmt.Errorf("failed to remove existing binary at %s: %w\n\nTry removing it manually: rm %s", destPath, err, destPath)
		}

		// Copy the binary
		if err := copyFile(execPath, destPath); err != nil {
			logger.Error("Failed to copy binary: %v", err)
			return fmt.Errorf("failed to copy binary: %w", err)
		}

		// Set executable permissions
		if err := os.Chmod(destPath, 0755); err != nil {
			logger.Error("Failed to set permissions: %v", err)
			return fmt.Errorf("failed to set executable permissions: %w", err)
		}

		fmt.Println("Confab installed successfully")
		fmt.Println()
	}

	// Check if destination directory is in PATH
	if isInPath(destDir) {
		fmt.Printf("%s is already in your PATH\n", destDir)
		fmt.Println("You can now run 'confab' from anywhere.")
	} else {
		fmt.Printf("%s is not in your PATH\n", destDir)
		fmt.Println()
		printPathInstructions(destDir)
	}

	logger.Info("Install complete")
	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// Create destination file (truncate if exists)
	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// isInPath checks if a directory is in the PATH environment variable
func isInPath(dir string) bool {
	pathEnv := os.Getenv("PATH")
	paths := filepath.SplitList(pathEnv)

	// Normalize the directory path
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		dirAbs = dir
	}

	for _, p := range paths {
		pAbs, err := filepath.Abs(p)
		if err != nil {
			pAbs = p
		}
		if pAbs == dirAbs {
			return true
		}
	}
	return false
}

// getShellName returns the name of the current shell (bash, zsh, fish, etc.)
func getShellName() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return "sh"
	}
	return filepath.Base(shell)
}

// printPathInstructions prints shell-specific instructions for adding a directory to PATH
func printPathInstructions(dir string) {
	shell := getShellName()

	// Convert absolute path back to $HOME-relative for display
	homeDir, _ := os.UserHomeDir()
	displayDir := dir
	if strings.HasPrefix(dir, homeDir) {
		displayDir = "$HOME" + dir[len(homeDir):]
	}

	fmt.Println("To add it, run:")
	fmt.Println()

	switch shell {
	case "fish":
		fmt.Printf("  echo 'set -gx PATH %s $PATH' >> ~/.config/fish/config.fish && source ~/.config/fish/config.fish\n", displayDir)
	case "zsh":
		fmt.Printf("  echo 'export PATH=\"%s:$PATH\"' >> ~/.zshrc && source ~/.zshrc\n", displayDir)
	case "bash":
		fmt.Printf("  echo 'export PATH=\"%s:$PATH\"' >> ~/.bashrc && source ~/.bashrc\n", displayDir)
	default:
		fmt.Printf("  echo 'export PATH=\"%s:$PATH\"' >> ~/.profile && source ~/.profile\n", displayDir)
	}
}

func init() {
	rootCmd.AddCommand(installCmd)
	installCmd.Flags().StringVar(&installDest, "dest", "", "custom installation directory (default: ~/.local/bin)")
}

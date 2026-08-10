package main

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

//go:embed ash_bootstrap/.ash_env
//go:embed ash_bootstrap/.ash_system
//go:embed ash_bootstrap/.ash_tools
//go:embed ash_bootstrap/.ash_bashrc
//go:embed ash_bootstrap/.ash_zshrc
//go:embed ash_bootstrap/tools/*
var embeddedBootstrapAssets embed.FS

func readEmbeddedBootstrapAsset(path string) ([]byte, error) {
	return embeddedBootstrapAssets.ReadFile(path)
}

func installEmbeddedBootstrapAssets(overwrite bool, stdout io.Writer) error {
	root, err := ashWorkspaceDir()
	if err != nil {
		return err
	}

	assetFiles := []struct {
		srcPath string
		dstPath string
		mode    fs.FileMode
	}{
		{srcPath: "ash_bootstrap/.ash_env", dstPath: filepath.Join(root, ".ash_env"), mode: 0o600},
		{srcPath: "ash_bootstrap/.ash_system", dstPath: filepath.Join(root, systemFileName), mode: 0o600},
		{srcPath: "ash_bootstrap/.ash_tools", dstPath: filepath.Join(root, toolsFileName), mode: 0o600},
		{srcPath: "ash_bootstrap/.ash_bashrc", dstPath: filepath.Join(root, ".ash_bashrc"), mode: 0o600},
		{srcPath: "ash_bootstrap/.ash_zshrc", dstPath: filepath.Join(root, ".ash_zshrc"), mode: 0o600},
	}

	for _, asset := range assetFiles {
		content, err := readEmbeddedBootstrapAsset(asset.srcPath)
		if err != nil {
			return fmt.Errorf("read %s: %w", asset.srcPath, err)
		}
		if err := installManagedAssetFile(asset.dstPath, content, overwrite, asset.mode, stdout, false); err != nil {
			return err
		}
	}

	entries, err := fs.ReadDir(embeddedBootstrapAssets, "ash_bootstrap/tools")
	if err != nil {
		return fmt.Errorf("read embedded tools directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		srcPath := filepath.ToSlash(filepath.Join("ash_bootstrap", "tools", entry.Name()))
		dstPath := filepath.Join(root, "tools", entry.Name())
		content, err := readEmbeddedBootstrapAsset(srcPath)
		if err != nil {
			return fmt.Errorf("read %s: %w", srcPath, err)
		}
		if err := installManagedAssetFile(dstPath, content, overwrite, 0o600, stdout, true); err != nil {
			return err
		}
	}

	return nil
}

func installManagedAssetFile(dstPath string, content []byte, overwrite bool, mode fs.FileMode, stdout io.Writer, isScript bool) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o700); err != nil {
		return fmt.Errorf("create parent dir for %s: %w", dstPath, err)
	}

	_, err := os.Stat(dstPath)
	if err == nil {
		if !overwrite {
			if stdout != nil {
				fmt.Fprintf(stdout, "kept existing %s\n", dstPath)
			}
			return applyAssetFilePermissions(dstPath, mode, isScript)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", dstPath, err)
	}

	if err := os.WriteFile(dstPath, content, mode); err != nil {
		return fmt.Errorf("write %s: %w", dstPath, err)
	}
	return applyAssetFilePermissions(dstPath, mode, isScript)
}

func applyAssetFilePermissions(path string, mode fs.FileMode, isScript bool) error {
	if isScript {
		mode = 0o700
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}

	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("resolve current user: %w", err)
	}
	uid, err := strconv.Atoi(currentUser.Uid)
	if err != nil {
		return fmt.Errorf("parse uid %q: %w", currentUser.Uid, err)
	}
	gid, err := strconv.Atoi(currentUser.Gid)
	if err != nil {
		return fmt.Errorf("parse gid %q: %w", currentUser.Gid, err)
	}
	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}
	return nil
}

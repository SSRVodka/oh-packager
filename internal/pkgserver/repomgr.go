package pkgserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/SSRVodka/oh-packager/internal/common"
	"github.com/SSRVodka/oh-packager/pkg/meta"
)

// RepoManager handles repository operations
type RepoManager struct {
	rootPath string
}

func NewRepoManager(rootPath string) *RepoManager {
	return &RepoManager{rootPath: rootPath}
}

// InitRepository creates the repository directory structure
func (rm *RepoManager) InitRepository(versions []string, architectures []string) error {
	// Create base directories
	dirs := []string{
		filepath.Join(rm.rootPath, "pool", "main"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Create version-specific directories
	for _, version := range versions {
		for _, arch := range architectures {
			distPath := filepath.Join(rm.rootPath, "dists", version, arch)
			if err := os.MkdirAll(distPath, 0755); err != nil {
				return fmt.Errorf("failed to create dist directory %s: %w", distPath, err)
			}

			// Initialize empty package index
			index := meta.RepositoryIndex{
				Repository:   version,
				Architecture: arch,
				LastUpdated:  time.Now().UTC().Format(time.RFC3339),
				Packages:     []meta.PackageIndex{},
			}

			if err := rm.savePackageIndex(version, arch, &index); err != nil {
				return err
			}
		}
	}

	fmt.Printf("✓ Repository initialized at: %s\n", rm.rootPath)
	fmt.Printf("✓ Created versions: %v\n", versions)
	fmt.Printf("✓ Architectures: %v\n", architectures)

	return nil
}

// DeployPackage deploys a package to the repository
func (rm *RepoManager) DeployPackage(pkgPath, version, arch string) error {
	// Verify package file exists
	if _, err := os.Stat(pkgPath); os.IsNotExist(err) {
		return fmt.Errorf("package file not found: %s", pkgPath)
	}

	// Extract metadata from package
	metadata, err := rm.extractMetadata(pkgPath)
	if err != nil {
		return fmt.Errorf("failed to extract metadata: %w", err)
	}

	// Validate version and architecture
	if metadata.Architecture != arch {
		return fmt.Errorf("architecture mismatch: package is %s, target is %s", metadata.Architecture, arch)
	}

	// Calculate SHA256
	hash, size, err := rm.calculateHash(pkgPath)
	if err != nil {
		return fmt.Errorf("failed to calculate hash: %w", err)
	}

	// Generate target filename
	filename := common.GenPkgFileName(metadata.Name, metadata.Version, metadata.Architecture)
	poolPath := filepath.Join(rm.rootPath, "pool", "main", filename)

	// Copy package to pool
	if err := rm.copyFile(pkgPath, poolPath); err != nil {
		return fmt.Errorf("failed to copy package: %w", err)
	}

	// Update repository index
	if err := rm.updateIndex(version, arch, metadata, filename, hash, size); err != nil {
		return fmt.Errorf("failed to update index: %w", err)
	}

	fmt.Printf("✓ Deployed: %s\n", filename)
	fmt.Printf("  Version: %s/%s\n", version, arch)
	fmt.Printf("  SHA256: %s\n", hash)
	fmt.Printf("  Size: %d bytes\n", size)

	return nil
}

// extractMetadata reads metadata from package file
func (rm *RepoManager) extractMetadata(pkgPath string) (*meta.PackageMetadata, error) {
	// Look for metadata.json in the same directory
	metadataPath := filepath.Join(filepath.Dir(pkgPath), "metadata.json")

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("metadata.json not found alongside package: %w", err)
	}

	var metadata meta.PackageMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("invalid metadata format: %w", err)
	}

	// Validate required fields
	if metadata.Name == "" || metadata.Version == "" || metadata.Architecture == "" {
		return nil, fmt.Errorf("missing required fields in metadata")
	}

	return &metadata, nil
}

// calculateHash computes SHA256 hash and size of file
func (rm *RepoManager) calculateHash(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()

	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}

	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

// copyFile copies a file from src to dst
func (rm *RepoManager) copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}

// updateIndex adds package to repository index
func (rm *RepoManager) updateIndex(version, arch string, metadata *meta.PackageMetadata, filename, hash string, size int64) error {
	index, err := rm.loadPackageIndex(version, arch)
	if err != nil {
		return err
	}

	// Remove existing package with same name (update scenario)
	filtered := []meta.PackageIndex{}
	for _, pkg := range index.Packages {
		if pkg.Name != metadata.Name {
			filtered = append(filtered, pkg)
		}
	}

	// Add new package
	pkgIndex := meta.PackageIndex{
		Name:         metadata.Name,
		Version:      metadata.Version,
		Architecture: metadata.Architecture,
		Filename:     filepath.Join("pool", "main", filename),
		SHA256:       hash,
		Size:         size,
		Dependencies: metadata.Dependencies,
		Description:  metadata.Description,
	}

	filtered = append(filtered, pkgIndex)
	index.Packages = filtered
	index.LastUpdated = time.Now().UTC().Format(time.RFC3339)

	return rm.savePackageIndex(version, arch, index)
}

// loadPackageIndex reads the package index from disk
func (rm *RepoManager) loadPackageIndex(version, arch string) (*meta.RepositoryIndex, error) {
	indexPath := filepath.Join(rm.rootPath, "dists", version, arch, "Packages.json")

	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, err
	}

	var index meta.RepositoryIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}

	return &index, nil
}

// savePackageIndex writes the package index to disk
func (rm *RepoManager) savePackageIndex(version, arch string, index *meta.RepositoryIndex) error {
	indexPath := filepath.Join(rm.rootPath, "dists", version, arch, "Packages.json")

	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(indexPath, data, 0644)
}

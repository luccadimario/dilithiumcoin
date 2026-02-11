package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// cmdInit creates a new wallet (first-time setup)
func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	walletDir := fs.String("wallet", DefaultWalletDir, "Wallet directory")
	force := fs.Bool("force", false, "Overwrite existing wallet")
	fs.Parse(args)

	// Check if wallet already exists
	privateKeyPath := filepath.Join(*walletDir, "private.pem")
	if _, err := os.Stat(privateKeyPath); err == nil && !*force {
		fmt.Println("Wallet already exists!")
		fmt.Printf("Location: %s\n", *walletDir)
		fmt.Println("\nUse --force to overwrite (THIS WILL DELETE YOUR EXISTING WALLET)")
		os.Exit(1)
	}

	fmt.Println("Creating new wallet...")
	fmt.Println()

	// Generate 2048-bit RSA key pair
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		fmt.Printf("Error generating key pair: %v\n", err)
		os.Exit(1)
	}

	// Create address from public key hash
	pubKeyBytes := privateKey.PublicKey.N.Bytes()
	hash := sha256.Sum256(pubKeyBytes)
	address := hex.EncodeToString(hash[:])[:40]

	// Create wallet directory
	if err := os.MkdirAll(*walletDir, 0700); err != nil {
		fmt.Printf("Error creating directory: %v\n", err)
		os.Exit(1)
	}

	// Save private key
	privateKeyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		fmt.Printf("Error encoding private key: %v\n", err)
		os.Exit(1)
	}

	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateKeyBytes,
	})

	if err := os.WriteFile(privateKeyPath, privateKeyPEM, 0600); err != nil {
		fmt.Printf("Error saving private key: %v\n", err)
		os.Exit(1)
	}

	// Save public key
	publicKeyPath := filepath.Join(*walletDir, "public.pem")
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		fmt.Printf("Error encoding public key: %v\n", err)
		os.Exit(1)
	}

	publicKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyBytes,
	})

	if err := os.WriteFile(publicKeyPath, publicKeyPEM, 0644); err != nil {
		fmt.Printf("Error saving public key: %v\n", err)
		os.Exit(1)
	}

	// Save address for convenience
	addressPath := filepath.Join(*walletDir, "address")
	if err := os.WriteFile(addressPath, []byte(address), 0644); err != nil {
		fmt.Printf("Error saving address: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("========================================")
	fmt.Println("        WALLET CREATED SUCCESSFULLY")
	fmt.Println("========================================")
	fmt.Println()
	fmt.Printf("  Address: %s\n", address)
	fmt.Println()
	fmt.Printf("  Location: %s\n", *walletDir)
	fmt.Println()
	fmt.Println("========================================")
	fmt.Println()
	fmt.Println("IMPORTANT: Back up your wallet directory!")
	fmt.Println("If you lose your private key, you lose your funds.")
	fmt.Println()
	fmt.Println("Quick start:")
	fmt.Println("  dilithium-cli balance          # Check your balance")
	fmt.Println("  dilithium-cli send <to> <amt>  # Send DLT")
}

// cmdAddress shows the wallet address
func cmdAddress(args []string) {
	fs := flag.NewFlagSet("address", flag.ExitOnError)
	walletDir := fs.String("wallet", DefaultWalletDir, "Wallet directory")
	fs.Parse(args)

	address, err := loadAddress(*walletDir)
	if err != nil {
		fmt.Println("No wallet found. Create one with:")
		fmt.Println("  dilithium-cli init")
		os.Exit(1)
	}

	fmt.Println(address)
}

// cmdWalletInfo shows detailed wallet information
func cmdWalletInfo(args []string) {
	fs := flag.NewFlagSet("wallet info", flag.ExitOnError)
	walletDir := fs.String("wallet", DefaultWalletDir, "Wallet directory")
	keyFile := fs.String("key", "", "Specific key file to inspect")
	fs.Parse(args)

	// If specific key file provided, use old behavior
	if *keyFile != "" {
		showKeyInfo(*keyFile)
		return
	}

	// Otherwise show wallet directory info
	address, err := loadAddress(*walletDir)
	if err != nil {
		fmt.Println("No wallet found. Create one with:")
		fmt.Println("  dilithium-cli init")
		os.Exit(1)
	}

	privateKeyPath := filepath.Join(*walletDir, "private.pem")
	publicKeyPath := filepath.Join(*walletDir, "public.pem")

	fmt.Println("========== WALLET INFO ==========")
	fmt.Printf("Address:      %s\n", address)
	fmt.Printf("Location:     %s\n", *walletDir)
	fmt.Printf("Private Key:  %s\n", privateKeyPath)
	fmt.Printf("Public Key:   %s\n", publicKeyPath)
	fmt.Println("==================================")
}

// cmdWalletExport exports wallet keys
func cmdWalletExport(args []string) {
	fs := flag.NewFlagSet("wallet export", flag.ExitOnError)
	walletDir := fs.String("wallet", DefaultWalletDir, "Wallet directory")
	fs.Parse(args)

	privateKeyPath := filepath.Join(*walletDir, "private.pem")
	privateKeyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		fmt.Println("No wallet found. Create one with:")
		fmt.Println("  dilithium-cli init")
		os.Exit(1)
	}

	address, _ := loadAddress(*walletDir)

	fmt.Println("========== WALLET EXPORT ==========")
	fmt.Printf("Address: %s\n", address)
	fmt.Println()
	fmt.Println("Private Key (KEEP SECRET!):")
	fmt.Println(string(privateKeyData))
	fmt.Println("===================================")
}

// showKeyInfo displays information about a specific key file
func showKeyInfo(keyFile string) {
	keyData, err := os.ReadFile(keyFile)
	if err != nil {
		fmt.Printf("Error reading key file: %v\n", err)
		os.Exit(1)
	}

	block, _ := pem.Decode(keyData)
	if block == nil {
		fmt.Println("Error: Invalid PEM format")
		os.Exit(1)
	}

	var publicKey *rsa.PublicKey

	switch block.Type {
	case "PRIVATE KEY":
		privateKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			fmt.Printf("Error parsing private key: %v\n", err)
			os.Exit(1)
		}
		rsaPrivateKey, ok := privateKey.(*rsa.PrivateKey)
		if !ok {
			fmt.Println("Error: Key is not an RSA private key")
			os.Exit(1)
		}
		publicKey = &rsaPrivateKey.PublicKey
		fmt.Println("Key Type: Private Key (RSA)")

	case "PUBLIC KEY":
		pubKey, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			fmt.Printf("Error parsing public key: %v\n", err)
			os.Exit(1)
		}
		var ok bool
		publicKey, ok = pubKey.(*rsa.PublicKey)
		if !ok {
			fmt.Println("Error: Key is not an RSA public key")
			os.Exit(1)
		}
		fmt.Println("Key Type: Public Key (RSA)")

	default:
		fmt.Printf("Error: Unknown PEM block type: %s\n", block.Type)
		os.Exit(1)
	}

	// Derive address
	pubKeyBytes := publicKey.N.Bytes()
	hash := sha256.Sum256(pubKeyBytes)
	address := hex.EncodeToString(hash[:])[:40]

	fmt.Println("\n========== KEY INFO ==========")
	fmt.Printf("Address:    %s\n", address)
	fmt.Printf("Key Size:   %d bits\n", publicKey.Size()*8)
	fmt.Printf("Key File:   %s\n", keyFile)
	fmt.Println("===============================")
}

// loadAddress loads the address from wallet directory
func loadAddress(walletDir string) (string, error) {
	// Try address file first
	addressPath := filepath.Join(walletDir, "address")
	if data, err := os.ReadFile(addressPath); err == nil {
		return string(data), nil
	}

	// Fall back to deriving from private key
	privateKeyPath := filepath.Join(walletDir, "private.pem")
	keyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return "", err
	}

	block, _ := pem.Decode(keyData)
	if block == nil {
		return "", fmt.Errorf("invalid PEM format")
	}

	privateKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", err
	}

	rsaPrivateKey, ok := privateKey.(*rsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("not an RSA key")
	}

	pubKeyBytes := rsaPrivateKey.PublicKey.N.Bytes()
	hash := sha256.Sum256(pubKeyBytes)
	return hex.EncodeToString(hash[:])[:40], nil
}

// loadPrivateKey loads the private key from wallet directory
func loadPrivateKey(walletDir string) (*rsa.PrivateKey, error) {
	privateKeyPath := filepath.Join(walletDir, "private.pem")
	keyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("could not read private key: %w", err)
	}

	block, _ := pem.Decode(keyData)
	if block == nil {
		return nil, fmt.Errorf("invalid PEM format")
	}

	privateKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("could not parse private key: %w", err)
	}

	rsaPrivateKey, ok := privateKey.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not an RSA private key")
	}

	return rsaPrivateKey, nil
}

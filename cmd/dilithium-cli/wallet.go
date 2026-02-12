package main

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudflare/circl/sign/dilithium/mode3"
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

	fmt.Println("Creating new quantum-safe wallet...")
	fmt.Println()

	// Generate CRYSTALS-Dilithium Mode3 key pair (192-bit quantum-safe)
	publicKey, privateKey, err := mode3.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Printf("Error generating key pair: %v\n", err)
		os.Exit(1)
	}

	// Create address from public key hash
	pubKeyBytes, _ := publicKey.MarshalBinary()
	hash := sha256.Sum256(pubKeyBytes)
	address := hex.EncodeToString(hash[:])[:40]

	// Create wallet directory
	if err := os.MkdirAll(*walletDir, 0700); err != nil {
		fmt.Printf("Error creating directory: %v\n", err)
		os.Exit(1)
	}

	// Ask for passphrase to encrypt private key
	fmt.Print("Enter passphrase to encrypt wallet (leave empty for no encryption): ")
	passphrase := readPassphrase()

	privKeyBytes, _ := privateKey.MarshalBinary()

	if passphrase != "" {
		fmt.Print("Confirm passphrase: ")
		confirm := readPassphrase()
		if passphrase != confirm {
			fmt.Println("Passphrases do not match.")
			os.Exit(1)
		}

		// Encrypt private key with passphrase
		encrypted, err := encryptKey(privKeyBytes, passphrase)
		if err != nil {
			fmt.Printf("Error encrypting private key: %v\n", err)
			os.Exit(1)
		}
		privateKeyPEM := pem.EncodeToMemory(&pem.Block{
			Type:  "DILITHIUM ENCRYPTED PRIVATE KEY",
			Bytes: encrypted,
		})
		if err := os.WriteFile(privateKeyPath, privateKeyPEM, 0600); err != nil {
			fmt.Printf("Error saving private key: %v\n", err)
			os.Exit(1)
		}
	} else {
		privateKeyPEM := pem.EncodeToMemory(&pem.Block{
			Type:  "DILITHIUM PRIVATE KEY",
			Bytes: privKeyBytes,
		})
		if err := os.WriteFile(privateKeyPath, privateKeyPEM, 0600); err != nil {
			fmt.Printf("Error saving private key: %v\n", err)
			os.Exit(1)
		}
	}

	// Save public key as PEM
	publicKeyPath := filepath.Join(*walletDir, "public.pem")
	publicKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "DILITHIUM PUBLIC KEY",
		Bytes: pubKeyBytes,
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
	fmt.Printf("  Algorithm: CRYSTALS-Dilithium Mode3\n")
	fmt.Printf("  Security:  192-bit (quantum-safe)\n")
	fmt.Printf("  Address:   %s\n", address)
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

	encrypted := "No"
	if keyData, err := os.ReadFile(privateKeyPath); err == nil {
		if block, _ := pem.Decode(keyData); block != nil && block.Type == "DILITHIUM ENCRYPTED PRIVATE KEY" {
			encrypted = "Yes"
		}
	}

	fmt.Println("========== WALLET INFO ==========")
	fmt.Printf("Algorithm:    CRYSTALS-Dilithium Mode3\n")
	fmt.Printf("Security:     192-bit (quantum-safe)\n")
	fmt.Printf("Address:      %s\n", address)
	fmt.Printf("Encrypted:    %s\n", encrypted)
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

	// Warn user before displaying private key
	fmt.Println("WARNING: This will display your PRIVATE KEY in the terminal.")
	fmt.Println("Anyone who sees this key can steal all your funds.")
	fmt.Println()
	fmt.Println("Make sure:")
	fmt.Println("  - No one is looking at your screen")
	fmt.Println("  - Your terminal is not being recorded or logged")
	fmt.Println("  - You are not sharing your screen")
	fmt.Println()
	fmt.Print("Type 'yes' to continue: ")

	var confirm string
	fmt.Scanln(&confirm)
	if confirm != "yes" {
		fmt.Println("Export cancelled.")
		return
	}

	fmt.Println()
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

	var address string

	switch block.Type {
	case "DILITHIUM PRIVATE KEY":
		var sk mode3.PrivateKey
		if err := sk.UnmarshalBinary(block.Bytes); err != nil {
			fmt.Printf("Error parsing private key: %v\n", err)
			os.Exit(1)
		}
		pk := sk.Public().(*mode3.PublicKey)
		pubKeyBytes, _ := pk.MarshalBinary()
		hash := sha256.Sum256(pubKeyBytes)
		address = hex.EncodeToString(hash[:])[:40]
		fmt.Println("Key Type: Private Key (CRYSTALS-Dilithium Mode3)")

	case "DILITHIUM PUBLIC KEY":
		var pk mode3.PublicKey
		if err := pk.UnmarshalBinary(block.Bytes); err != nil {
			fmt.Printf("Error parsing public key: %v\n", err)
			os.Exit(1)
		}
		hash := sha256.Sum256(block.Bytes)
		address = hex.EncodeToString(hash[:])[:40]
		fmt.Println("Key Type: Public Key (CRYSTALS-Dilithium Mode3)")

	default:
		fmt.Printf("Error: Unknown PEM block type: %s\n", block.Type)
		os.Exit(1)
	}

	fmt.Println("\n========== KEY INFO ==========")
	fmt.Printf("Algorithm:  CRYSTALS-Dilithium Mode3\n")
	fmt.Printf("Security:   192-bit (quantum-safe)\n")
	fmt.Printf("Address:    %s\n", address)
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

	var sk mode3.PrivateKey
	if err := sk.UnmarshalBinary(block.Bytes); err != nil {
		return "", fmt.Errorf("could not parse private key: %w", err)
	}

	pk := sk.Public().(*mode3.PublicKey)
	pubKeyBytes, _ := pk.MarshalBinary()
	hash := sha256.Sum256(pubKeyBytes)
	return hex.EncodeToString(hash[:])[:40], nil
}

// loadPrivateKey loads the private key from wallet directory
func loadPrivateKey(walletDir string) (*mode3.PrivateKey, error) {
	privateKeyPath := filepath.Join(walletDir, "private.pem")
	keyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("could not read private key: %w", err)
	}

	block, _ := pem.Decode(keyData)
	if block == nil {
		return nil, fmt.Errorf("invalid PEM format")
	}

	var privKeyBytes []byte

	if block.Type == "DILITHIUM ENCRYPTED PRIVATE KEY" {
		// Encrypted key — prompt for passphrase
		fmt.Print("Enter wallet passphrase: ")
		passphrase := readPassphrase()
		decrypted, err := decryptKey(block.Bytes, passphrase)
		if err != nil {
			return nil, fmt.Errorf("decryption failed (wrong passphrase?): %w", err)
		}
		privKeyBytes = decrypted
	} else {
		privKeyBytes = block.Bytes
	}

	var sk mode3.PrivateKey
	if err := sk.UnmarshalBinary(privKeyBytes); err != nil {
		return nil, fmt.Errorf("could not parse private key: %w", err)
	}

	return &sk, nil
}

// readPassphrase reads a line from stdin (passphrase input)
func readPassphrase() string {
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}

// deriveKey derives a 32-byte AES key from a passphrase and salt using SHA-256 stretching
func deriveKey(passphrase string, salt []byte) []byte {
	// Simple key stretching: SHA-256(salt || passphrase) iterated 100,000 times
	key := sha256.Sum256(append(salt, []byte(passphrase)...))
	for i := 0; i < 100_000; i++ {
		key = sha256.Sum256(key[:])
	}
	return key[:]
}

// encryptKey encrypts data with AES-256-GCM using a passphrase
func encryptKey(plaintext []byte, passphrase string) ([]byte, error) {
	// Generate random 16-byte salt
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}

	key := deriveKey(passphrase, salt)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// Format: salt(16) || nonce(12) || ciphertext
	result := make([]byte, 0, len(salt)+len(nonce)+len(ciphertext))
	result = append(result, salt...)
	result = append(result, nonce...)
	result = append(result, ciphertext...)
	return result, nil
}

// decryptKey decrypts data encrypted with encryptKey
func decryptKey(data []byte, passphrase string) ([]byte, error) {
	if len(data) < 28 { // 16 salt + 12 nonce minimum
		return nil, fmt.Errorf("encrypted data too short")
	}

	salt := data[:16]
	key := deriveKey(passphrase, salt)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < 16+nonceSize {
		return nil, fmt.Errorf("encrypted data too short")
	}

	nonce := data[16 : 16+nonceSize]
	ciphertext := data[16+nonceSize:]

	return gcm.Open(nil, nonce, ciphertext, nil)
}

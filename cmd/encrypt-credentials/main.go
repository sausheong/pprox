package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	inputFile := flag.String("input", "", "Input credential file (JSON)")
	outputFile := flag.String("output", "", "Output encrypted file")
	encryptionKey := flag.String("key", "", "Encryption key (32 bytes)")
	decrypt := flag.Bool("decrypt", false, "Decrypt instead of encrypt")

	flag.Parse()

	if *inputFile == "" || *outputFile == "" || *encryptionKey == "" {
		fmt.Println("Usage:")
		fmt.Println("  Encrypt: ./encrypt-credentials -input creds.json -output creds.enc -key 'your-32-byte-key'")
		fmt.Println("  Decrypt: ./encrypt-credentials -input creds.enc -output creds.json -key 'your-32-byte-key' -decrypt")
		os.Exit(1)
	}

	// Derive 32-byte key
	key := make([]byte, 32)
	copy(key, []byte(*encryptionKey))

	if *decrypt {
		if err := decryptFile(*inputFile, *outputFile, key); err != nil {
			fmt.Printf("Decryption failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Successfully decrypted %s to %s\n", *inputFile, *outputFile)
	} else {
		if err := encryptFile(*inputFile, *outputFile, key); err != nil {
			fmt.Printf("Encryption failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Successfully encrypted %s to %s\n", *inputFile, *outputFile)
		fmt.Println("\nIMPORTANT: Store the encryption key securely!")
		fmt.Println("Set it as environment variable: CREDENTIAL_ENCRYPTION_KEY")
	}
}

func encryptFile(inputPath, outputPath string, key []byte) error {
	// Read input file
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read input file: %w", err)
	}

	// Create cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}

	// Generate nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}

	// Encrypt
	ciphertext := gcm.Seal(nonce, nonce, data, nil)

	// Write output file
	if err := os.WriteFile(outputPath, ciphertext, 0600); err != nil {
		return fmt.Errorf("failed to write encrypted file: %w", err)
	}

	return nil
}

func decryptFile(inputPath, outputPath string, key []byte) error {
	// Read encrypted file
	ciphertext, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read encrypted file: %w", err)
	}

	// Create cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}

	// Extract nonce
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]

	// Decrypt
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return fmt.Errorf("decryption failed: %w", err)
	}

	// Write output file
	if err := os.WriteFile(outputPath, plaintext, 0600); err != nil {
		return fmt.Errorf("failed to write decrypted file: %w", err)
	}

	return nil
}

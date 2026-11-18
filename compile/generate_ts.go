package compile

import (
	"context"
	"fmt"
	"log"
	"midnight-go-sdk/utils"
	"os"
	"path/filepath"
	"text/template"
)

// Generate writes a state reader atomically so concurrent reads cannot execute
// a partially written script. Endpoints and credentials are passed through env.
func (c *Config) Generate(ctx context.Context, contractAddress, contractImportPath, destPath string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("generate state reader canceled: %w", err)
	}
	readerPath, err := filepath.Abs(filepath.Join(c.StaticDir, "state.mjs"))
	if err != nil {
		return fmt.Errorf("resolve state reader path: %w", err)
	}
	data := struct{ ContractImportPath, ContractAddress, StateReaderPath string }{
		contractImportPath, utils.TrimHex(utils.ConvertWithNetworkIdAccount(contractAddress)), readerPath,
	}
	tmpl, err := template.ParseFiles(filepath.Join(c.StaticDir, "contract.tmpl"))
	if err != nil {
		return fmt.Errorf("parse contract state template: %w", err)
	}
	file, err := os.CreateTemp(destPath, ".state-reader-*.ts")
	if err != nil {
		return fmt.Errorf("create contract state reader: %w", err)
	}
	defer func() {
		if err := os.Remove(file.Name()); err != nil && !os.IsNotExist(err) {
			log.Printf("remove temporary state reader: %v", err)
		}
	}()
	writeErr := tmpl.Execute(file, data)
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("render contract state reader: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close contract state reader: %w", closeErr)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("generate state reader canceled: %w", err)
	}
	if err := os.Rename(file.Name(), filepath.Join(destPath, "generated_demo.ts")); err != nil {
		return fmt.Errorf("publish contract state reader: %w", err)
	}
	return nil
}

func (c *Config) GenerateIndexTs(destPath string) error {
	templateFile := fmt.Sprintf("%s/%s", c.StaticDir, "index.ts.tmpl")
	outputFile := fmt.Sprintf("%s/index.ts", destPath)

	tmpl, err := template.ParseFiles(templateFile)
	if err != nil {
		return fmt.Errorf("error to parse templete %s: %v", templateFile, err)
	}

	file, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("can't output file %s: %v", outputFile, err)
	}
	defer file.Close()

	err = tmpl.Execute(file, map[string]string{})
	if err != nil {
		return fmt.Errorf("error to execute template: %v", err)
	}

	log.Printf("success generate %s", outputFile)
	return nil
}

//go:build none
// +build none

package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	"github.com/BlockdagNetworkLabs/bdag/params"
)

func main() {
	bdagDir := filepath.Clean(filepath.Join(currentScriptDir(), "..", "bdag"))
	defaultGenesis := filepath.Join(bdagDir, "genesis.json")
	// defaultBurnList := filepath.Clean("./../bdag/burn_list.json")

	genesisPath := flag.String("genesis", defaultGenesis, "path to the genesis.json template")
	// burnListPath := flag.String("burnlist", defaultBurnList, "path to the burn_list.json template")
	flag.Parse()

	genesisJ, err := readFromFile(*genesisPath)
	if err != nil {
		log.Fatalf("read genesis json (%s): %v", *genesisPath, err)
	}
	// burnListJ, err := readFromFile(*burnListPath)
	// if err != nil {
	// 	log.Fatalf("read burn list json (%s): %v", *burnListPath, err)
	// }

	fileHeader := "// It is called by go generate and used to automatically generate pre-computed \n// This file is auto generate by : go run mkalloc.go \npackage bdag\n\n"

	if err := saveFile(filepath.Join(bdagDir, "genesis_alloc.go"),
		fileHeader+
			fmt.Sprintf("\nconst genesisJson = `%s`", genesisJ),
		// fmt.Sprintf("\nconst burnListJson = `%s`", burnListJ),
	); err != nil {
		log.Fatal(err)
	}

	hashContent := fileHeader
	for _, np := range params.AllNetParams {
		alloc := bdag.DoDecodeAlloc(np.Params, genesisJ, "")
		genesis := bdag.Genesis(np.Net, alloc)
		genesisHash := genesis.ToBlock().Hash()
		log.Printf("network = %s, genesisHash= %s\n", np.Name, genesisHash.String())
		hashContent += fmt.Sprintf("\nconst %sGenesisHash = \"%s\"", np.Net.String(), genesisHash.String())
	}

	if err := saveFile(filepath.Join(bdagDir, "genesis_hash.go"), hashContent); err != nil {
		log.Fatal(err)
	}
}

func readFromFile(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var buff bytes.Buffer
	if _, err := buff.ReadFrom(file); err != nil {
		return "", err
	}
	return buff.String(), nil
}

func currentScriptDir() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		log.Fatal("cannot determine script directory")
	}
	return filepath.Dir(filename)
}

func saveFile(fileName string, fileContent string) error {
	f, err := os.Create(fileName)
	if err != nil {
		return fmt.Errorf("Save error:%s  %s", fileName, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	if _, err := f.WriteString(fileContent); err != nil {
		return fmt.Errorf("write error:%s %s", fileName, err)
	}

	fmt.Println("Successfully updated:", fileName)
	return nil
}

package main

// Quick sanity test for eggcompat config parsers (run via go run).

import (
	"fmt"
	"os"

	"ptero-native/internal/eggcompat"
)

func main() {
	dir := "/tmp/eggcfg-test"
	_ = os.MkdirAll(dir, 0o755)
	defer func() { _ = os.RemoveAll(dir) }()

	// properties
	_ = os.WriteFile(dir+"/server.properties", []byte("server-port=25565\nmotd=hello\n"), 0o644)
	_ = eggcompat.ApplyEggConfig(dir, "server.properties", "properties",
		[]map[string]string{}, map[string]interface{}{"server-port": "25566", "max-players": 20})
	b, _ := os.ReadFile(dir + "/server.properties")
	fmt.Println("properties:", string(b))

	// yaml
	_ = os.WriteFile(dir+"/config.yml", []byte("server:\n  port: 25565\n  name: test\n"), 0o644)
	_ = eggcompat.ApplyEggConfig(dir, "config.yml", "yaml",
		[]map[string]string{}, map[string]interface{}{"server.port": 25567})
	b, _ = os.ReadFile(dir + "/config.yml")
	fmt.Println("yaml:", string(b))

	// json
	_ = os.WriteFile(dir+"/config.json", []byte(`{"server":{"port":25565}}`), 0o644)
	_ = eggcompat.ApplyEggConfig(dir, "config.json", "json",
		[]map[string]string{}, map[string]interface{}{"server.port": 25568})
	b, _ = os.ReadFile(dir + "/config.json")
	fmt.Println("json:", string(b))

	// file (literal replace)
	_ = os.WriteFile(dir+"/app.conf", []byte("listen on 127.0.0.1:8080\n"), 0o644)
	_ = eggcompat.ApplyEggConfig(dir, "app.conf", "file",
		[]map[string]string{{"match": "127.0.0.1:8080", "replace_with": "0.0.0.0:9000"}}, nil)
	b, _ = os.ReadFile(dir + "/app.conf")
	fmt.Println("file:", string(b))
}

package compile

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"midnight-go-sdk/config"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

const compactContract = `
pragma language_version >= 0.16 && <= 0.21;

import CompactStandardLibrary;

enum State {
  VACANT,
  OCCUPIED
}

struct NumberAnd {
  num: Uint<32>;
  item: Uint<32>;
}

export ledger state: State;

export ledger message: Maybe<Opaque<"string">>;

export ledger sequence: Counter;

export ledger owner: Bytes<32>;

constructor() {
  state = State.VACANT;
  message = none<Opaque<"string">>();
  sequence.increment(1);
}

witness localSecretKey(): Bytes<32>;

export circuit post(newMessage: Opaque<"string">): [] {
  assert(state == State.VACANT, "Attempted to post to an occupied board");
  owner = disclose(publicKey(localSecretKey(), sequence as Field as Bytes<32>));
  message = disclose(some<Opaque<"string">>(newMessage));
  state = State.OCCUPIED;
}

export ledger items: MerkleTree<10, Field>;

export circuit insert(item: Field): [] {
  items.insert(disclose(item));
}

export circuit takeDown(): Opaque<"string"> {
  assert(state == State.OCCUPIED, "Attempted to take down post from an empty board");
  assert(owner == publicKey(localSecretKey(), sequence as Field as Bytes<32>), "Attempted to take down post, but not the current owner");
  const formerMsg = message.value;
  state = State.VACANT;
  sequence.increment(1);
  message = none<Opaque<"string">>();
  return formerMsg;
}

export circuit publicKey(sk: Bytes<32>, sequence: Bytes<32>): Bytes<32> {
  return persistentHash<Vector<3, Bytes<32>>>([pad(32, "bboard:pk:"), sequence, sk]);
}
`

func InitConf() *Config {
	conf := &Config{}
	conf.StaticDir = "../static"
	conf.CompilerVersion = "0.29.0"
	conf.TempDir = "../tmp"
	var err error
	conf.NetworkConf, err = config.NewNetworkConfig()
	if err != nil {
		panic(err)
	}
	return conf
}

func TestComplieAndExec(t *testing.T) {
	conf := InitConf()
	ctx := context.Background()

	// clean up before test
	_ = os.RemoveAll("../tmp/0x456")
	// _ = os.RemoveAll("../static/verified/0x456")

	t.Run("compile contract", func(t *testing.T) {
		sourceCode := compactContract
		err := conf.CompileContract(ctx, conf.CompilerVersion, "0x456", sourceCode, "single_file")
		assert.NoError(t, err)
	})
	t.Run("read verify key", func(t *testing.T) {
		bytes, err := os.ReadFile("../static/verified/0x456/src/managed/keys/post.verifier")
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, fmt.Sprintf("%x", sha1.Sum(bytes)), "febb5451235e5c7cc533d7013cbacc3bc27c98e1")

		bytes2, err := os.ReadFile("../static/verified/0x456/src/managed/keys/takeDown.verifier")
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, fmt.Sprintf("%x", sha1.Sum(bytes2)), "8da7d64baa7632ec55a2bd9528b2d528c5e80746")
	})

	t.Run("Generate and exec ts script", func(t *testing.T) {
		err := conf.Generate(ctx, "02007f4931e5944402a8f27f45760ad56942a7c2e7c6241d7d0d4d788683de07f1fb", "./src/managed", "../static/verified/0x456")
		assert.NoError(t, err)
		assert.NoError(t, conf.GenerateIndexTs("../static/verified/0x456/src/managed"))
	})

	t.Run("Exec ts script", func(t *testing.T) {
		if os.Getenv("RUN_TS_SCRIPT_TESTS") == "" {
			t.Skip("set RUN_TS_SCRIPT_TESTS=1 to run the Preview indexer integration test")
		}
		output, err := conf.ExecTsScript(context.Background(), "../static/verified/0x456/generated_demo.ts")
		assert.NoError(t, err)
		fmt.Println("Exec output:", output)
		var state ContractStateResponse
		err = json.Unmarshal([]byte(output), &state)
		assert.NoError(t, err)
		assert.GreaterOrEqual(t, len(state.MaintenanceAuthority.Committee), 1)
		items := state.Data["items"]
		assert.Equal(t, "347026af970487f54011d170b0a3dfe9d1e6fdd07788029a5e63c4f4031bb6ee", items.(string))
	})
}

func Test_parseCompactContract(t *testing.T) {
	contract, err := ParseCompactContract(SingleFile, compactContract)
	if err != nil {
		assert.FailNow(t, err.Error())
	}
	bytesValue, _ := json.Marshal(contract)
	assert.Equal(t, "{\"ledger\":{\"items\":\"MerkleTree\\u003c10, Field\\u003e\",\"message\":\"Maybe\\u003cOpaque\\u003c\\\"string\\\"\\u003e\\u003e\",\"owner\":\"Bytes\\u003c32\\u003e\",\"sequence\":\"Counter\",\"state\":\"State\"},\"enums\":{\"State\":{\"members\":[\"VACANT\",\"OCCUPIED\"]}},\"structs\":{\"NumberAnd\":{\"fields\":[{\"name\":\"num\",\"type\":\"Uint\\u003c32\\u003e\"},{\"name\":\"item\",\"type\":\"Uint\\u003c32\\u003e\"}]}}}", string(bytesValue))

	standardInput := `
	{
	  "contracts":{
	    "board.compact":"pragma language_version >= 0.16 && <= 0.17;\n\nimport CompactStandardLibrary;\n\ninclude \"board2\";\n\nconstructor() {\n  state = State.VACANT;\n  message = none<Opaque<\"string\">>();\n  sequence.increment(1);\n}\n\nwitness localSecretKey(): Bytes<32>;\n\nexport circuit post(newMessage: Opaque<\"string\">): [] {\n  assert(state == State.VACANT, \"Attempted to post to an occupied board\");\n  owner = disclose(publicKey(localSecretKey(), sequence as Field as Bytes<32>));\n  message = disclose(some<Opaque<\"string\">>(newMessage));\n  state = State.OCCUPIED;\n}\n\n\n\nexport circuit publicKey(sk: Bytes<32>, sequence: Bytes<32>): Bytes<32> {\n  return persistentHash<Vector<3, Bytes<32>>>([pad(32, \"bboard:pk:\"), sequence, sk]);\n}",
	    "board2.compact":"pragma language_version >= 0.16 && <= 0.17;\n\nimport CompactStandardLibrary;\n\nenum State {\n  VACANT,\n  OCCUPIED\n}\n\nstruct NumberAnd {\n  num: Uint<32>;\n  item: Uint<32>;\n}\n\nexport ledger state: State;\n\nexport ledger message: Maybe<Opaque<\"string\">>;\n\nexport ledger sequence: Counter;\n\nexport ledger owner: Bytes<32>;\n\nexport ledger items: MerkleTree<10, Field>;\n\nexport circuit takeDown(): Opaque<\"string\"> {\n  assert(state == State.OCCUPIED, \"Attempted to take down post from an empty board\");\n  assert(owner == publicKey(localSecretKey(), sequence as Field as Bytes<32>), \"Attempted to take down post, but not the current owner\");\n  const formerMsg = message.value;\n  state = State.VACANT;\n  sequence.increment(1);\n  message = none<Opaque<\"string\">>();\n  return formerMsg;\n}\n\nexport circuit insert(item: Field): [] {\n  items.insert(disclose(item));\n}"
	  },
	  "entry-file":"board.compact"
	}`
	contract, err = ParseCompactContract(MultiFile, standardInput)
	b, _ := json.Marshal(contract)
	assert.Equal(t, "{\"ledger\":{\"items\":\"MerkleTree\\u003c10, Field\\u003e\",\"message\":\"Maybe\\u003cOpaque\\u003c\\\"string\\\"\\u003e\\u003e\",\"owner\":\"Bytes\\u003c32\\u003e\",\"sequence\":\"Counter\",\"state\":\"State\"},\"enums\":{\"State\":{\"members\":[\"VACANT\",\"OCCUPIED\"]}},\"structs\":{\"NumberAnd\":{\"fields\":[{\"name\":\"num\",\"type\":\"Uint\\u003c32\\u003e\"},{\"name\":\"item\",\"type\":\"Uint\\u003c32\\u003e\"}]}}}", string(b))
}

func TestParseStandardInputJSON(t *testing.T) {
	t.Run("valid standard-input-json", func(t *testing.T) {
		stdInput, err := parseStandardInputJSON(`{
		  "contracts": {
		    "count.compact": "pragma language_version >= 0.20;\n"
		  },
		  "entry-file": "count.compact"
		}`)
		assert.NoError(t, err)
		assert.Equal(t, "count.compact", stdInput.EntryFile)
		assert.Equal(t, "pragma language_version >= 0.20;\n", stdInput.Contracts["count.compact"])
	})

	t.Run("invalid standard-input-json reports decode error", func(t *testing.T) {
		_, err := parseStandardInputJSON(`{
		  "contracts": {
		    "count.compact": "pragma language_version >= 0.20;\n",
		  },
		  "entry-file": "count.compact"
		}`)
		assert.EqualError(t, err, "decode standard-input-json: invalid character '}' looking for beginning of object key string")
	})
}

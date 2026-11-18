# Convert compact contract to standard_input_json
# gist: https://gist.github.com/freehere107/7f9cbb664d2aec6d36ac88d1a591fa04
# Example
# python standard_json_input.py --entryFile xx.compact

import argparse
import os
import json

parser = argparse.ArgumentParser(description='Print all files in the specified directory')

parser.add_argument('--entryFile', type=str, default="index.compact", help='entryFile name')
parser.add_argument('--exclude', type=str, nargs='+', default=[], help='exclude folder')

args = parser.parse_args()
extension = ["compact"]
exclude = [".idea", ".git", "target"] + args.exclude

inputJson = {
    "entry-file": args.entryFile,
    "contracts": {}
}

for root, dirs, files in os.walk("."):
    dirs[:] = [d for d in dirs if d not in exclude]
    for file in files:
        if file.endswith(tuple(extension)):
            filePath = os.path.join(root, file)
            with open(filePath, 'r') as f:
                contents = f.read()
                if contents == "":
                    continue
            inputJson["contracts"][filePath.lstrip("./")] = contents

## check entry file exists
if args.entryFile not in inputJson["contracts"]:
    raise Exception(f"Entry file {args.entryFile} not found in contracts")

print(json.dumps(inputJson, sort_keys=True, indent=4, separators=(',', ':')))
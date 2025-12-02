#!/bin/bash

set -Eeuo pipefail

dir=${1:-}
repo=${2:-}

if [ -z "$dir" ] || [ ! -d "$dir" ]; then
	echo "ERROR: not a valid directory: '$dir'"
	exit 1
fi
if [ -z "$repo" ] || [ ! -d "$repo" ]; then
	echo "ERROR: not a valid repo directory: '$repo'"
	exit 1
fi

find $dir -maxdepth 1 -name "*.json" | while read file; do
	name=$(basename "$file" .json)
	abs_dir=$(dirname $(realpath "$file"))
	echo "deploying $abs_dir/$name -> $repo"
	oh-pkgserver deploy $abs_dir/$name.pkg $abs_dir/$name.json --repo $repo
done


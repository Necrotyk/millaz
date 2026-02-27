import sys
import glob

def refactor(filename):
    with open(filename, "r") as f:
        content = f.read()

    # We need to make sure log/slog is imported where necessary
    
    with open(filename, "w") as f:
        f.write(content)

for f in glob.glob("conspiri/*.go"):
    refactor(f)

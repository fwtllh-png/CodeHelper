TIMEOUT_SECONDS = 30


def load(path):
    with open(path) as handle:
        return handle.read()

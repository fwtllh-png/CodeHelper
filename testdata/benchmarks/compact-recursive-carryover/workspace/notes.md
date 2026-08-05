# Greeting notes

The greeting string lives in service.py and is mirrored in settings.yaml.
Both are read at start-up, so the two must agree or the banner and the
API disagree about what the service calls itself.

Historically the greeting was hard-coded in three places. Two of them
were removed; settings.yaml is the remaining source of truth and
service.py is expected to follow it.

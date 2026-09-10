# The bash-completion implementation of _init_completion does not work through
# snap command wrappers, so force Cobra to use its basic internal one.
unset -f _init_completion

source <($SNAP/bin/cli completion bash)

# Bash completion for inference packaged in a snap.

# Force Cobra to use its basic internal implementation. The implementation
# provided by bash-completion does not work through snap command wrappers.
unset -f _init_completion

source <($SNAP/bin/inference completion bash)

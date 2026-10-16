# Source this file from ~/.bashrc, or install it as bash-completion's keyparcel file.
_keyparcel() {
    local -a words=()
    local i token previous='' candidate cur
    COMPREPLY=()
    # Delegate before reconstructing words: Readline needs the original path context.
    local path_index=$COMP_CWORD path_flag
    [[ ${COMP_WORDS[path_index]} == '=' || ${COMP_WORDS[path_index]} == ':' ]] && ((path_index--))
    while ((path_index > 1)) && [[ ${COMP_WORDS[path_index-1]} == '=' || ${COMP_WORDS[path_index-1]} == ':' ]]; do
        ((path_index-=2))
    done
    path_flag=${COMP_WORDS[path_index-1]:-}
    [[ ${COMP_WORDS[path_index]} == --*=* ]] && path_flag=${COMP_WORDS[path_index]%%=*}
    [[ ${COMP_WORDS[path_index]} == --* && $path_index -lt $COMP_CWORD ]] && path_flag=${COMP_WORDS[path_index]}
    case $path_flag in
        --file|--save-to|--identity|--config|--ssh-config)
            local path_word='' path_start=$path_index expanded prefix='' assignment_prefix=''
            [[ ${COMP_WORDS[path_start]} == --* && ${COMP_WORDS[path_start]} != *=* ]] && ((path_start+=2))
            for ((i=path_start; i<=COMP_CWORD; i++)); do
                path_word+=${COMP_WORDS[i]}
            done
            [[ $path_word == --*=* ]] && path_word=${path_word#*=}
            expanded=$path_word
            [[ $expanded == '~/'* ]] && expanded=$HOME/${expanded:2}
            if [[ ${COMP_WORDS[COMP_CWORD]} == --*=* ]]; then
                assignment_prefix=${COMP_WORDS[COMP_CWORD]%%=*}=
            elif [[ ${COMP_WORDS[COMP_CWORD]} == '=' || ${COMP_WORDS[COMP_CWORD]} == ':' ]]; then
                prefix=$path_word
            elif [[ ${COMP_WORDS[COMP_CWORD]} != "$path_word" ]]; then
                prefix=${path_word%"${COMP_WORDS[COMP_CWORD]}"}
            fi
            while IFS= read -r candidate; do
                [[ $path_word == '~/'* ]] && candidate="~/"${candidate#"$HOME/"}
                COMPREPLY+=("$assignment_prefix${candidate#"$prefix"}")
            done < <(compgen -f -- "$expanded")
            compopt -o filenames 2>/dev/null || true
            return ;;
    esac
    # Bash splits unquoted fingerprints at ':' and flag assignments at '='.
    for ((i=1; i<=COMP_CWORD; i++)); do
        token=${COMP_WORDS[i]}
        if [[ $token == '=' && ${#words[@]} -gt 0 ]]; then
            words[${#words[@]}-1]+=$token
            previous=$token
            continue
        elif [[ $token == ':' && ${#words[@]} -gt 0 ]]; then
            words[${#words[@]}-1]+=$token
        elif [[ $previous == ':' || $previous == '=' ]] && ((${#words[@]})); then
            words[${#words[@]}-1]+=$token
        else
            words+=("$token")
        fi
        previous=$token
    done
    cur=${words[${#words[@]}-1]:-}
    while IFS= read -r candidate; do
        # Readline replaces only the portion following a word-break colon.
        if [[ ${COMP_WORDS[COMP_CWORD]} != "$cur" && $cur == *:* ]]; then
            candidate=${candidate#*:}
        elif [[ ${COMP_WORDS[COMP_CWORD]} != "$cur" && $cur == *=* ]]; then
            candidate=${candidate#"${cur%%=*}="}
        fi
        COMPREPLY+=("$candidate")
    done < <(command keyparcel __complete "${words[@]}" 2>/dev/null)
    if ((${#COMPREPLY[@]})); then
        # Names/comments are literal values, never executable shell syntax.
        for i in "${!COMPREPLY[@]}"; do
            printf -v 'COMPREPLY[i]' '%q' "${COMPREPLY[i]}"
        done
        compopt -o noquote 2>/dev/null || true
    fi
}
complete -F _keyparcel keyparcel

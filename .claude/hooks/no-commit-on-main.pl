#!/usr/bin/perl
# PreToolUse hook: deny `git add`, `git commit` and `git merge` when the
# repository the command acts on is on main.
#
# The repository is worked out per git invocation, not from the session's
# working directory, so a command aimed at a worktree is judged by the
# worktree's branch:
#   - `git -C <path> commit` uses <path> (several -C options chain, as in git)
#   - otherwise a leading `cd <path> &&` (or `;`) sets the directory
#   - otherwise the session's working directory (`cwd` in the hook input)
use strict;
use warnings;
use JSON::PP;

# git's own errors (a -C path that doesn't exist yet) are not the hook's output.
open(STDERR, '>', '/dev/null');

my $input = do { local $/; <STDIN> };
my $data  = eval { decode_json($input) } or exit 0;
my $cmd   = $data->{tool_input}{command} // '';
my $cwd   = $data->{cwd} || $ENV{CLAUDE_PROJECT_DIR} || '.';

my $path = qr/("[^"]*"|'[^']*'|[^\s;&|()]+)/;

# Turn a path as written in the command into an absolute one, relative to $base.
sub resolve {
    my ($p, $base) = @_;
    $p =~ s/^"(.*)"$/$1/s or $p =~ s/^'(.*)'$/$1/s;
    $p =~ s{^~(?=/|$)}{$ENV{HOME} // '~'}e;
    return $p =~ m{^/} ? $p : "$base/$p";
}

my $base = $cwd;
$base = resolve($1, $cwd) if $cmd =~ /^\s*cd\s+$path\s*(?:&&|;)/;

# Global options git accepts before the subcommand. -C is captured so it can
# be applied; the others are skipped so they don't hide the subcommand.
my $opt = qr/\s+-C\s+$path|\s+-c\s+\S+|\s+--[\w-]+(?:=\S+)?|\s+-[A-Za-z]+/;

my @dirs;
while ($cmd =~ /(?:^|[\s;&|(])git((?:$opt)*)\s+(add|commit|merge)(?=$|[\s;&|)])/g) {
    my $opts = $1;
    my $dir  = $base;
    while ($opts =~ /-C\s+$path/g) {
        $dir = resolve($1, $dir);
    }
    push @dirs, $dir;
}

for my $dir (@dirs) {
    open(my $fh, '-|', 'git', '-C', $dir, 'rev-parse', '--abbrev-ref', 'HEAD')
        or next;
    my $branch = <$fh> // '';
    close $fh;
    chomp $branch;
    next unless $branch eq 'main';
    print encode_json({
        hookSpecificOutput => {
            hookEventName            => 'PreToolUse',
            permissionDecision       => 'deny',
            permissionDecisionReason => "Blocked by CLAUDE.md workflow: never commit to main ($dir is on main). Create a topic branch first: git switch -c <topic> (or git worktree add .worktrees/<topic> -b <topic>).",
        },
    });
    exit 0;
}
exit 0;

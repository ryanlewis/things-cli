#!/usr/bin/perl
# PreToolUse hook: when a `git commit` stages CLI code or the bundled skill
# but no docs page, remind the agent to update the docs. Warning only: it
# never denies.
#
# The staged files are read from the repository each commit targets, not from
# the session's working directory. The target is worked out the same way as in
# no-commit-on-main.pl (keep the two in step):
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
while ($cmd =~ /(?:^|[\s;&|(])git((?:$opt)*)\s+commit(?=$|[\s;&|)])/g) {
    my $opts = $1;
    my $dir  = $base;
    while ($opts =~ /-C\s+$path/g) {
        $dir = resolve($1, $dir);
    }
    push @dirs, $dir;
}

# -a/--all also commits the unstaged changes to tracked files.
my $all = $cmd =~ /(^|\s)(--all|-[a-zA-Z]*a[a-zA-Z]*)(\s|$)/;

sub git_lines {
    my ($dir, @args) = @_;
    open(my $fh, '-|', 'git', '-C', $dir, @args) or return;
    chomp(my @lines = <$fh>);
    close $fh;
    return @lines;
}

for my $dir (@dirs) {
    my @files = git_lines($dir, 'diff', '--cached', '--name-only');
    push @files, git_lines($dir, 'diff', '--name-only') if $all;
    next if grep { m{^docs/content/} } @files;
    next unless grep { !/_test\.go$/ && m{^(?:cmd/things/[^/]+\.go|internal/skill/SKILL\.md)$} } @files;
    print encode_json({
        hookSpecificOutput => {
            hookEventName     => 'PreToolUse',
            additionalContext => 'The changes about to be committed touch cmd/things/*.go or internal/skill/SKILL.md but nothing under docs/content/. Check whether this changes the surface of a subcommand: a command, flag, argument, output shape, or config key. If it does, update internal/skill/SKILL.md and the matching page under docs/content/ (commands.md for commands and flags, configuration.md for config keys, agents.md for agent-facing behaviour, install.md for install methods) before committing. Reminder only, not a block.',
        },
    });
    exit 0;
}
exit 0;

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
#
# Text inside quotes or a heredoc body is data, not a command, so `echo "git
# commit"` is left alone. (A `bash -c "git commit"` is therefore not seen.)
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

# Copy of a command with quoted text and heredoc bodies blanked to 'x', same
# length, so offsets in the copy are offsets in the original. Quote characters
# stay, so a quoted path still looks like a path.
sub mask {
    my ($s) = @_;
    my ($out, $q, @heredocs) = ('', '');
    my ($i, $n) = (0, length $s);
    while ($i < $n) {
        my $c = substr($s, $i, 1);
        if ($q) {
            if ($c eq '\\' && $q eq '"' && $i + 1 < $n) {
                $out .= 'xx';
                $i += 2;
                next;
            }
            if ($c eq $q) {
                $q = '';
                $out .= $c;
            } else {
                $out .= $c eq "\n" ? $c : 'x';
            }
            $i++;
            next;
        }
        if ($c eq '\\' && $i + 1 < $n) {
            $out .= substr($s, $i, 2);
            $i += 2;
            next;
        }
        if ($c eq '#' && ($i == 0 || substr($s, $i - 1, 1) =~ /[\s;&|(]/)) {
            # A comment runs to end of line; an apostrophe in it opens no quote.
            my $e = index($s, "\n", $i);
            $e = $n if $e < 0;
            $out .= 'x' x ($e - $i);
            $i = $e;
            next;
        }
        if ($c eq '"' || $c eq "'") {
            $q = $c;
            $out .= $c;
            $i++;
            next;
        }
        if (substr($s, $i, 3) eq '<<<') {
            $out .= '<<<';
            $i += 3;
            next;
        }
        if (substr($s, $i) =~ /^<<(-?)\s*(?:'(\w+)'|"(\w+)"|(\w+))/) {
            push @heredocs, [$1, $2 // $3 // $4];
            $out .= substr($s, $i, length $&);
            $i += length $&;
            next;
        }
        $out .= $c;
        $i++;
        next unless $c eq "\n" && @heredocs;
        for my $h (@heredocs) {
            my ($dash, $delim) = @$h;
            while ($i < $n) {
                my $e    = index($s, "\n", $i);
                my $len  = ($e < 0 ? $n : $e) - $i;
                my $line = substr($s, $i, $len);
                $out .= 'x' x $len;
                $i += $len;
                $out .= "\n", $i++ if $i < $n;
                $line =~ s/^\t+// if $dash;
                last if $line eq $delim;
            }
        }
        @heredocs = ();
    }
    return $out;
}

my $base = $cwd;
$base = resolve($1, $cwd) if $cmd =~ /^\s*cd\s+$path\s*(?:&&|;)/;

# Global options git accepts before the subcommand. -C is captured so it can
# be applied; the others are skipped so they don't hide the subcommand.
my $opt = qr/\s+-C\s+$path|\s+-c\s+\S+|\s+--[\w-]+(?:=\S+)?|\s+-[A-Za-z]+/;

my @dirs;
my $masked = mask($cmd);
while ($masked =~ /(?:^|[\s;&|(])git((?:$opt)*)\s+(add|commit|merge)(?=$|[\s;&|)])/g) {
    my $opts = substr($cmd, $-[1], $+[1] - $-[1]);
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

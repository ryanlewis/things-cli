#!/usr/bin/perl
# Tests for no-commit-on-main.pl: run `prove .claude/hooks/no-commit-on-main.t`.
# Each case feeds the hook a command as JSON on stdin, with the working
# directory on a repo that is on main or on a topic branch.
use strict;
use warnings;
use File::Basename qw(dirname);
use File::Temp qw(tempdir);
use JSON::PP;
use Test::More;

my $hook = dirname(__FILE__) . '/no-commit-on-main.pl';
my $tmp  = tempdir(CLEANUP => 1);

sub repo {
    my ($name, $branch) = @_;
    my $dir = "$tmp/$name";
    system('git', 'init', '-q', '-b', $branch, $dir) == 0 or die "git init";
    # The hook reads the branch with rev-parse, which needs a commit.
    system('git', '-C', $dir, '-c', 'user.name=t', '-c', 'user.email=t@t',
        'commit', '-q', '--allow-empty', '-m', 'init') == 0 or die "git commit";
    return $dir;
}

my $main  = repo('on-main', 'main');
my $topic = repo('on-topic', 'topic');

# Returns true if the hook denies the command.
sub blocked {
    my ($cwd, $cmd) = @_;
    my $in = "$tmp/in.json";
    open(my $fh, '>', $in) or die;
    print $fh encode_json({ cwd => $cwd, tool_input => { command => $cmd } });
    close $fh;
    my $out = `perl '$hook' < '$in'`;
    return $out =~ /"permissionDecision":"deny"/ ? 1 : 0;
}

# [ command, blocked with cwd on main, blocked with cwd on a topic branch ]
my @cases = (
    # Quoted text is not a command.
    [ 'echo "git commit -m x"',                                0, 0 ],
    [ q{echo 'git add . && git merge x'},                      0, 0 ],
    [ 'gh issue create --body "run git commit"',               0, 0 ],
    [ 'cat <<< "git commit"',                                  0, 0 ],
    [ 'echo "git commit" # note',                              0, 0 ],
    # Heredoc bodies are not commands.
    [ "gh issue create --body-file - <<EOF\nrun git commit\nEOF\n", 0, 0 ],
    [ "cat <<-EOF\n\tgit add .\n\tEOF\n",                      0, 0 ],
    # Real commands.
    [ 'git commit -m x',                                       1, 0 ],
    [ 'git add .',                                             1, 0 ],
    [ 'git merge origin/main',                                 1, 0 ],
    [ 'git status && git merge origin/main',                   1, 0 ],
    [ 'echo hi; git commit -m x',                              1, 0 ],
    [ '(git add .)',                                           1, 0 ],
    [ 'git commit -m "feat: x"',                               1, 0 ],
    [ q{git commit -m "it's fine"},                            1, 0 ],
    [ 'git -c user.name=x commit -m x',                        1, 0 ],
    # A real command after quoted or heredoc text is still seen.
    [ "git commit -m \"\$(cat <<'EOF'\nfeat: x\n\nbody\nEOF\n)\"", 1, 0 ],
    [ "cat <<EOF\ngit commit\nEOF\ngit commit -m x",           1, 0 ],
    [ "# it's a note\ngit commit -m x",                        1, 0 ],
    [ q{git commit -m "a # b" # it's},                         1, 0 ],
    # -C and cd aim the command at another repo.
    [ "git -C $main commit -m x",                              1, 1 ],
    [ "git -C \"$main\" add .",                                1, 1 ],
    [ "git -C $topic commit -m x",                             0, 0 ],
    [ "cd $main && git commit -m x",                           1, 1 ],
    [ "cd $topic && git commit -m x",                          0, 0 ],
    [ "cd $topic; git commit -m x",                            0, 0 ],
);

for my $case (@cases) {
    my ($cmd, $on_main, $on_topic) = @$case;
    (my $label = $cmd) =~ s/\n/~/g;
    is(blocked($main,  $cmd), $on_main,  "main:  $label");
    is(blocked($topic, $cmd), $on_topic, "topic: $label");
}

done_testing;

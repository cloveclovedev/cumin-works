# 作業場所の片付け

cuminは、閉じたsub-issueのworktreeを自動で消す ([Agentの実行の設計](../designs/agent-run.md) の「作業場所」)。次のworktreeは、自動では消えない。

- 閉じた要求Issueのsub-issueのもの。cuminは閉じた要求Issueを読まない。
- Maintainerが要求Issueから外したsub-issueのもの。
- GitHubにない作業を持っていたので、cuminが残したもの。ログに `cleanup: the worktree of a closed issue holds work that is not on GitHub; it stays` が出ている。

worktreeが残っても、動作には影響しない。使うのはディスクだけである。ビルドの成果物があると、1つで数GBになることがある。

## 残っているworktreeを見つける

対象のリポジトリごとに、cloneで一覧を出す。`<work_dir>` は設定 `work_dir` の値である。

```sh
git -C <work_dir>/<owner>/<repo>/clone worktree list
```

ディレクトリの名前は `<Issue番号>-<role>` である。そのIssueがGitHubで閉じているか、どの開いている要求Issueのsub-issueでもないかを確かめる。

## 消す前に確かめる

GitHubにない作業を持っていないかを見る。

```sh
cd <work_dir>/<owner>/<repo>/<Issue番号>-<role>
git status --short
git log --oneline HEAD --not --remotes=origin
```

どちらも何も出なければ、消してよい。何か出たら、要るものならpushするか、別の場所にコピーしてから消す。

## 消す

cloneの中で消す。Implementerのworktreeなら、ローカルのブランチも消す。

```sh
git -C <work_dir>/<owner>/<repo>/clone worktree remove --force <work_dir>/<owner>/<repo>/<Issue番号>-<role>
git -C <work_dir>/<owner>/<repo>/clone branch -D cumin/<Issue番号>-<短い説明>
```

Hostの状態ファイル (`~/.local/state/cumin/state.json`) にそのIssueの項目が残っていても、消さなくてよい。項目は小さく、Maintainerが `cumin/status/ready` を付け直したときに、cuminが消す。

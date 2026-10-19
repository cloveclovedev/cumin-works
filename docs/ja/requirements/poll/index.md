# 定期確認

cumin が GitHub をいつ、どの順に読み、どの Issue を扱うかを決める。

## 全体像

このカテゴリは、まだ書いている途中である。

## 要件

### 扱う Issue

#### `CWK-POLL-001` 閉じた要求Issueには何もしない

cumin は、閉じた要求Issueを読まず、ラベルを替えず、コメントも書かない。

- 検証: `test` 閉じた要求Issueに、cumin は GitHub への書き込みをしない

---

## 未決事項

- 旧版の cumin-core.md の「受け持つこと」「GitHubの呼び出しの失敗」「全部を読めないIssue」「状態の持ち方」と、issue-states.md の「原則」「着手の順番」を、このカテゴリに書く。

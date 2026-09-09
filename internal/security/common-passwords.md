# The embedded breach corpus

`common-passwords.txt` is the list `IsBreachedPassword` checks against. It is
compiled into the binary, so the check costs no network call and tells nobody
outside this machine that somebody is registering.

## Where it comes from

The source is the `xato-net-10-million-passwords-1000000.txt` list distributed
with [SecLists](https://github.com/danielmiessler/SecLists), which is Mark
Burnett's public-domain release of passwords gathered from breach dumps,
ordered by how often they occur.

## How it was filtered

Amici refuses any password under `PasswordMinLen` characters, so most of a
breach corpus can never reach the check. The list here is what is left after
filtering, and it is why twenty thousand entries is a real defence rather than
a gesture:

    awk 'length($0) >= 10 && length($0) <= 64' xato-net-10-million-passwords-1000000.txt \
      | head -20000 \
      | LC_ALL=C sort -u \
      > common-passwords.txt

`head` is applied before `sort` on purpose. The source is in frequency order,
so taking the first twenty thousand keeps the passwords people actually use,
and sorting afterwards makes the file diffable when it is next refreshed.

## Refreshing it

Re-run the pipeline above against a current list. Two things to check
afterwards:

  * `fixtures.Password` must not be in it, or every fixture account fails to
    hash its password and local development stops working. There is a test for
    this.
  * The file must stay ASCII and one entry per line. `loadCorpus` skips blank
    lines and lines starting with `#`, and nothing else.

Growing the list is cheap in disk and memory but not free: it is loaded into a
map on first use. If it ever needs to be an order of magnitude larger, the
right answer is a bloom filter rather than a bigger map.

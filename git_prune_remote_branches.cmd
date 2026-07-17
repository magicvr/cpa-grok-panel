@echo off

REM prune remote branches
git remote prune origin

REM Return to original directory
popd

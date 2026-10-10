The command/flag tables and regular expressions are native policy data from src/tools/BashTool/readOnlyValidation.ts and src/utils/shell/readOnlyCommandValidation.ts, plus safe environment names from src/tools/BashTool/bashPermissions.ts. Go owns token interpretation and classification; this data contains no executable Source business logic.

Shell parsing uses the unchanged MIT shell-quote 1.8.3 parse.js in an isolated Goja VM with no filesystem, process, module-loading or network capabilities. Its license is included.

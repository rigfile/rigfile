package gitmod

// GlobalIgnore is the base-secure global gitignore (RIGFILE_PLAN.md §8.1a). Anything here is ignored in every
// repository on the machine. Some entries (*.sqlite, .npmrc, *.tfvars) are legitimate in some repos: the
// pre-commit scanner only BLOCKS them when they hold secrets, so a reviewed file can still be added with
// `git add -f`.
const GlobalIgnore = `# managed by rigfile (base-secure): credential and key files are never tracked by accident.
# Override for a reviewed file with: git add -f <file>
.env
.env.*
!.env.example
!.env.sample
!.env.template
!.env.dist
*.pem
*.key
*.p12
*.pfx
*.jks
*.keystore
id_rsa*
id_ed25519*
id_ecdsa*
!id_rsa*.pub
!id_ed25519*.pub
!id_ecdsa*.pub
*.ppk
credentials.json
*credentials*.json
service-account*.json
*-sa.json
*adminsdk*.json
client_secret*.json
token.json
.npmrc
.pypirc
.netrc
.htpasswd
.aws/
.gcp/
.azure/
kubeconfig
*.kubeconfig
*.tfstate
*.tfstate.*
*.tfvars
!*.example.tfvars
secrets.y*ml
*.secret
*.sqlite
*.db
.DS_Store
`

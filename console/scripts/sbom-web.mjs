// Inventory bundled runtime packages and preserve original legal notices.
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)))
const web = path.join(root, 'web')
const release = path.join(root, 'release')
fs.mkdirSync(release, { recursive: true })
const npm = process.platform === 'win32' ? 'npm.cmd' : 'npm'
fs.writeFileSync(path.join(release, 'sbom-web.cdx.json'), execFileSync(npm, ['sbom', '--sbom-format', 'cyclonedx', '--omit=dev'], { cwd: web }))
const paths = [...new Set(execFileSync(npm, ['ls', '--omit=dev', '--all', '--parseable'], { cwd: web, encoding: 'utf8' }).trim().split('\n'))].sort()
const notices = []
const sections = ['Authova Console bundled runtime dependency notices\n']
let packages = 0
for (const directory of paths) {
  if (directory === web) continue
  const pkg = JSON.parse(fs.readFileSync(path.join(directory, 'package.json'), 'utf8'))
  const files = fs.readdirSync(directory).filter(name => /^(license|notice|copying|copyright)/i.test(name) && fs.statSync(path.join(directory, name)).isFile()).sort()
  if (!files.length) throw new Error('Missing upstream runtime notice for ' + pkg.name)
  const destination = path.join(release, 'notices', 'web', pkg.name.replaceAll('/', '__') + '@' + pkg.version)
  fs.mkdirSync(destination, { recursive: true })
  sections.push(`\n=== ${pkg.name}@${pkg.version} ===\n`)
  for (const file of files) {
    const data = fs.readFileSync(path.join(directory, file))
    const target = path.join(destination, file)
    fs.writeFileSync(target, data)
    sections.push(data.toString('utf8') + '\n')
    notices.push({ package: pkg.name, version: pkg.version, license: pkg.license, file: path.relative(root, target).split(path.sep).join('/'), sha256: createHash('sha256').update(data).digest('hex') })
  }
  packages++
}
fs.writeFileSync(path.join(release, 'dependency-notices-web.json'), JSON.stringify(notices, null, 2) + '\n')
fs.writeFileSync(path.join(release, 'THIRD-PARTY-NOTICES-WEB.txt'), sections.join(''))
if (fs.existsSync(path.join(web, 'dist'))) fs.writeFileSync(path.join(web, 'dist', 'third-party-notices.txt'), sections.join(''))
process.stdout.write(`Retained runtime web notices for ${packages} packages.\n`)

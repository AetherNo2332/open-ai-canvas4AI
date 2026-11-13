package architecture

// packageRoles classifies internal top-level packages. New packages require an
// explicit role; domain-to-domain imports retain the existing dependency rules.
var packageRoles = map[string]string{
	"handler": "handler", "service": "service", "app": "app",
	"repository": "repository", "model": "model", "database": "database",
	"agentcontext": "domain", "agentmemory": "domain", "architecture": "domain",
	"assets": "domain", "auth": "domain", "buildinfo": "domain",
	"canvas": "domain", "creationplatform": "domain", "ecommerceagent": "domain",
	"ecommercecatalog": "domain", "ecommerceknowledge": "domain", "ecommerceskills": "domain",
	"emailidentity": "domain", "emailtpl": "domain", "generation": "domain",
	"hostupdate": "domain", "identity": "domain", "inspirations": "domain",
	"jianying": "domain", "kernel": "domain", "logging": "domain",
	"observability": "domain", "outbound": "domain", "payment": "domain",
	"platform": "domain", "poselibrary": "domain", "production": "domain",
	"prompts": "domain", "protocol": "domain", "provider": "domain",
	"providercatalog": "domain", "skills": "domain", "sms": "domain",
	"storage": "domain", "taskruntime": "domain", "tools": "domain",
	"updaterclient": "domain", "workbench": "domain",
}

func (s *scanner) packageClassificationFindings() []Finding {
	seen := map[string]bool{}
	var findings []Finding
	for _, src := range s.files {
		pkg := layerOf(src.path)
		if pkg == "" || seen[pkg] || packageRoles[pkg] != "" {
			continue
		}
		seen[pkg] = true
		findings = append(findings, Finding{
			ID: "internal/" + pkg + ":package_classification", Rule: "package_classification", Value: 1,
			Detail: "classify the new internal package in architecture/package_roles.go before accepting its dependency role",
		})
	}
	return findings
}

import babel from "@babel/core";
import { fileURLToPath } from "node:url";
import fs from "node:fs";

export async function resolve(specifier, context, nextResolve) {
  try {
    return await nextResolve(specifier, context);
  } catch (err) {
    if (err.code === "ERR_MODULE_NOT_FOUND" && specifier.endsWith(".js")) {
      const jsxSpecifier = specifier.slice(0, -3) + ".jsx";
      return await nextResolve(jsxSpecifier, context);
    }
    throw err;
  }
}

export async function load(url, context, nextLoad) {
  if (url.endsWith(".css")) {
    return { format: "module", source: "export default {};", shortCircuit: true };
  }
  if (url.includes("?raw") || url.endsWith(".html")) {
    try {
      const filePath = fileURLToPath(url.replace(/\?raw$/, ""));
      const content = fs.readFileSync(filePath, "utf8");
      return { format: "module", source: `export default ${JSON.stringify(content)};`, shortCircuit: true };
    } catch {
      return { format: "module", source: 'export default "";', shortCircuit: true };
    }
  }
  if (url.endsWith(".jsx")) {
    const result = await nextLoad(url, { ...context, format: "module" });
    let source = result.source;
    if (typeof source !== "string") {
      source = new TextDecoder().decode(source);
    }
    const transformed = babel.transformSync(source, {
      plugins: [["@babel/plugin-transform-react-jsx", { pragma: "h", pragmaFrag: "Fragment" }]],
      filename: fileURLToPath(url),
      configFile: false,
      babelrc: false,
    });
    source = transformed.code;
    return { format: "module", source, shortCircuit: true };
  }
  return nextLoad(url, context);
}

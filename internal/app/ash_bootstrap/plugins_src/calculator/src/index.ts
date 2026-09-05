#!/usr/bin/env node

import * as fs from "node:fs";
import * as path from "node:path";
import * as process from "node:process";

const PLUGIN_NAME = "calculator";
const VERSION = "1.0.0";

function getAiDocs(): string {
  const docs = {
    Capabilities: [
      "Safely evaluate mathematical expressions and formulas.",
      "Support arithmetic (+, -, *, /, %, ^), parentheses, constants (pi, e), and standard math functions (sin, cos, tan, sqrt, log, ln, exp, abs, floor, ceil, round)."
    ],
    Arguments: {
      "--expr": "Mathematical expression string to evaluate (e.g. '2 + 2 * 3', 'sin(pi / 4)', 'sqrt(144) + 10^2')",
      "--format": "Output format: 'json' (default) or 'text'",
      "--ai-docs": "Print AI documentation and exit",
      "--version": "Print version and exit",
      "--help": "Print help and exit"
    },
    "Return format": {
      status: "success or error",
      expression: "the evaluated input expression",
      result: "calculated numerical result as a float or integer",
      error_message: "description of syntax or evaluation error when status is error"
    },
    "Usage guidance for the AI": "Use calculator for precise mathematical computations instead of performing mental math. A single call evaluates the expression. Directly parse and use the numerical result from the JSON response."
  };
  return JSON.stringify(docs, null, 2);
}

function logEvent(level: string, msg: string, fields: Record<string, string> = {}) {
  const logFile = process.env.ASH_LOG_FILE?.trim();
  if (!logFile) return;

  const verbose = process.env.ASH_VERBOSE?.toLowerCase() || "";
  const isVerbose = ["1", "true", "yes", "debug"].includes(verbose);
  if (level === "DEBUG" && !isVerbose) return;

  const format = (process.env.ASH_LOG_FORMAT || "json").toLowerCase();
  const dir = path.dirname(logFile);
  try {
    fs.mkdirSync(dir, { recursive: true });
  } catch {}

  const now = Math.floor(Date.now() / 1000);
  try {
    if (format === "json") {
      const entry = {
        time: now,
        level: level.toLowerCase(),
        plugin: PLUGIN_NAME,
        message: msg,
        ...fields
      };
      fs.appendFileSync(logFile, JSON.stringify(entry) + "\n");
    } else {
      const extra = Object.entries(fields)
        .map(([k, v]) => ` ${k}=${v}`)
        .join("");
      fs.appendFileSync(logFile, `[${now}] ${level} ${PLUGIN_NAME}: ${msg}${extra}\n`);
    }
  } catch {}
}

export class MathParser {
  private pos = 0;
  private tokens: string[] = [];

  constructor(private expr: string) {
    this.tokens = this.tokenize(expr);
  }

  private tokenize(str: string): string[] {
    const tokens: string[] = [];
    let i = 0;
    while (i < str.length) {
      const ch = str[i];
      if (/\s/.test(ch)) {
        i++;
        continue;
      }
      if (/[0-9.]/.test(ch)) {
        let num = "";
        while (i < str.length && /[0-9.]/.test(str[i])) {
          num += str[i];
          i++;
        }
        tokens.push(num);
        continue;
      }
      if (/[a-zA-Z_]/.test(ch)) {
        let ident = "";
        while (i < str.length && /[a-zA-Z0-9_]/.test(str[i])) {
          ident += str[i];
          i++;
        }
        tokens.push(ident.toLowerCase());
        continue;
      }
      if ("+-*/%^(),".includes(ch)) {
        tokens.push(ch);
        i++;
        continue;
      }
      throw new Error(`Unexpected character: '${ch}'`);
    }
    return tokens;
  }

  public parse(): number {
    this.pos = 0;
    if (this.tokens.length === 0) {
      throw new Error("Empty expression");
    }
    const res = this.parseExpression();
    if (this.pos < this.tokens.length) {
      throw new Error(`Unexpected token '${this.tokens[this.pos]}' at position ${this.pos}`);
    }
    return res;
  }

  private parseExpression(): number {
    let value = this.parseTerm();
    while (this.pos < this.tokens.length) {
      const op = this.tokens[this.pos];
      if (op === "+") {
        this.pos++;
        value += this.parseTerm();
      } else if (op === "-") {
        this.pos++;
        value -= this.parseTerm();
      } else {
        break;
      }
    }
    return value;
  }

  private parseTerm(): number {
    let value = this.parsePower();
    while (this.pos < this.tokens.length) {
      const op = this.tokens[this.pos];
      if (op === "*") {
        this.pos++;
        value *= this.parsePower();
      } else if (op === "/") {
        this.pos++;
        const denom = this.parsePower();
        if (denom === 0) throw new Error("Division by zero");
        value /= denom;
      } else if (op === "%") {
        this.pos++;
        const denom = this.parsePower();
        if (denom === 0) throw new Error("Modulo by zero");
        value %= denom;
      } else {
        break;
      }
    }
    return value;
  }

  private parsePower(): number {
    let value = this.parseFactor();
    if (this.pos < this.tokens.length && this.tokens[this.pos] === "^") {
      this.pos++;
      const exponent = this.parsePower(); // Right-associative
      value = Math.pow(value, exponent);
    }
    return value;
  }

  private parseFactor(): number {
    if (this.pos >= this.tokens.length) {
      throw new Error("Unexpected end of expression");
    }
    const token = this.tokens[this.pos];

    if (token === "+") {
      this.pos++;
      return this.parseFactor();
    }
    if (token === "-") {
      this.pos++;
      return -this.parseFactor();
    }
    if (token === "(") {
      this.pos++;
      const val = this.parseExpression();
      if (this.pos >= this.tokens.length || this.tokens[this.pos] !== ")") {
        throw new Error("Missing closing parenthesis ')'");
      }
      this.pos++;
      return val;
    }
    if (/^[0-9.]+$/.test(token)) {
      this.pos++;
      const num = parseFloat(token);
      if (isNaN(num)) throw new Error(`Invalid number '${token}'`);
      return num;
    }
    if (token === "pi") {
      this.pos++;
      return Math.PI;
    }
    if (token === "e") {
      this.pos++;
      return Math.E;
    }

    // Function calls
    if (/^[a-z0-9_]+$/.test(token)) {
      const fnName = token;
      this.pos++;
      if (this.pos >= this.tokens.length || this.tokens[this.pos] !== "(") {
        throw new Error(`Expected '(' after function '${fnName}'`);
      }
      this.pos++;
      const args: number[] = [];
      if (this.pos < this.tokens.length && this.tokens[this.pos] !== ")") {
        args.push(this.parseExpression());
        while (this.pos < this.tokens.length && this.tokens[this.pos] === ",") {
          this.pos++;
          args.push(this.parseExpression());
        }
      }
      if (this.pos >= this.tokens.length || this.tokens[this.pos] !== ")") {
        throw new Error(`Missing closing ')' for function '${fnName}'`);
      }
      this.pos++;
      return this.evalFunction(fnName, args);
    }

    throw new Error(`Unexpected token: '${token}'`);
  }

  private evalFunction(name: string, args: number[]): number {
    switch (name) {
      case "sin": return Math.sin(args[0]);
      case "cos": return Math.cos(args[0]);
      case "tan": return Math.tan(args[0]);
      case "sqrt":
        if (args[0] < 0) throw new Error("sqrt of negative number");
        return Math.sqrt(args[0]);
      case "abs": return Math.abs(args[0]);
      case "log":
      case "ln":
        if (args[0] <= 0) throw new Error("log of non-positive number");
        return Math.log(args[0]);
      case "log10":
        if (args[0] <= 0) throw new Error("log10 of non-positive number");
        return Math.log10(args[0]);
      case "exp": return Math.exp(args[0]);
      case "floor": return Math.floor(args[0]);
      case "ceil": return Math.ceil(args[0]);
      case "round": return Math.round(args[0]);
      case "min": return Math.min(...args);
      case "max": return Math.max(...args);
      default:
        throw new Error(`Unknown function: '${name}'`);
    }
  }
}

function main() {
  const rawArgs = process.argv.slice(2);

  if (rawArgs.length > 0) {
    if (rawArgs[0] === "--ai-docs") {
      console.log(getAiDocs());
      process.exit(0);
    }
    if (rawArgs[0] === "--version" || rawArgs[0] === "-v") {
      console.log(`${PLUGIN_NAME} ${VERSION}`);
      process.exit(0);
    }
    if (rawArgs[0] === "--help" || rawArgs[0] === "-h") {
      console.log(`${PLUGIN_NAME} - TypeScript math expression evaluator plugin for Ash\n\nUsage:\n  ${PLUGIN_NAME} [--expr "<expression>"] [--format text|json]\n\nFlags:\n  --expr EXPR   Mathematical expression to evaluate\n  --format FMT  Output format: 'text' (default) or 'json'\n  --ai-docs     Print AI documentation and exit\n  -v, --version Print version and exit\n  -h, --help    Print help and exit`);
      process.exit(0);
    }
  }

  logEvent("DEBUG", "executing calculator plugin", { EID: "calc01a" });

  let expr = "";
  let format = "json";

  for (let i = 0; i < rawArgs.length; i++) {
    if (rawArgs[i] === "--expr" && i + 1 < rawArgs.length) {
      expr = rawArgs[++i];
    } else if (rawArgs[i] === "--format" && i + 1 < rawArgs.length) {
      format = rawArgs[++i];
    } else if (!rawArgs[i].startsWith("-") && !expr) {
      expr = rawArgs[i];
    }
  }

  if (!expr.trim()) {
    logEvent("ERROR", "no expression provided", { EID: "calc02b" });
    if (format === "json") {
      console.log(JSON.stringify({ status: "error", error_message: "No expression provided. Use --expr '<expression>'" }, null, 2));
    } else {
      console.error("Error: No expression provided. Use --expr '<expression>'");
    }
    process.exit(1);
  }

  try {
    const parser = new MathParser(expr);
    const result = parser.parse();

    logEvent("DEBUG", "calculation succeeded", { EID: "calc03c", expr });

    if (format === "json") {
      console.log(JSON.stringify({
        status: "success",
        expression: expr,
        result: result
      }, null, 2));
    } else {
      console.log(result.toString());
    }
    process.exit(0);
  } catch (err: any) {
    const msg = err?.message || String(err);
    logEvent("ERROR", `calculation failed: ${msg}`, { EID: "calc04d", expr });
    if (format === "json") {
      console.log(JSON.stringify({
        status: "error",
        expression: expr,
        error_message: msg
      }, null, 2));
    } else {
      console.error(`Error: ${msg}`);
    }
    process.exit(1);
  }
}

if (process.argv[1] && (process.argv[1].endsWith("index.js") || process.argv[1].endsWith("calculator"))) {
  main();
}

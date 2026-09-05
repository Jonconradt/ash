import test from "node:test";
import assert from "node:assert/strict";
import { MathParser } from "./index.js";

test("Basic arithmetic and operator precedence", () => {
  assert.equal(new MathParser("2 + 3 * 4").parse(), 14);
  assert.equal(new MathParser("(2 + 3) * 4").parse(), 20);
  assert.equal(new MathParser("10 - 4 + 2").parse(), 8);
  assert.equal(new MathParser("100 / 10 / 2").parse(), 5);
  assert.equal(new MathParser("7 % 4").parse(), 3);
});

test("Exponentiation associativity", () => {
  // 2^3^2 = 2^(3^2) = 2^9 = 512 (standard right-associativity)
  assert.equal(new MathParser("2^3^2").parse(), 512);
  assert.equal(new MathParser("(2^3)^2").parse(), 64);
});

test("Unary negative and subtraction", () => {
  assert.equal(new MathParser("-5 + 10").parse(), 5);
  assert.equal(new MathParser("10 + -5").parse(), 5);
  assert.equal(new MathParser("-(3 + 4) * 2").parse(), -14);
  assert.equal(new MathParser("--5").parse(), 5);
});

test("Floating point decimals and scientific constants", () => {
  assert.ok(Math.abs(new MathParser("0.1 + 0.2").parse() - 0.3) < 1e-9);
  assert.equal(new MathParser("pi").parse(), Math.PI);
  assert.equal(new MathParser("e").parse(), Math.E);
  assert.ok(Math.abs(new MathParser("2 * pi * 10").parse() - 20 * Math.PI) < 1e-9);
});

test("Mathematical functions", () => {
  assert.equal(new MathParser("sin(0)").parse(), 0);
  assert.ok(Math.abs(new MathParser("sin(pi / 2)").parse() - 1) < 1e-9);
  assert.equal(new MathParser("cos(0)").parse(), 1);
  assert.equal(new MathParser("sqrt(144)").parse(), 12);
  assert.equal(new MathParser("abs(-42)").parse(), 42);
  assert.equal(new MathParser("floor(4.99)").parse(), 4);
  assert.equal(new MathParser("ceil(4.01)").parse(), 5);
  assert.equal(new MathParser("round(4.5)").parse(), 5);
  assert.equal(new MathParser("min(10, 5, 20)").parse(), 5);
  assert.equal(new MathParser("max(10, 5, 20)").parse(), 20);
  assert.ok(Math.abs(new MathParser("ln(e)").parse() - 1) < 1e-9);
  assert.ok(Math.abs(new MathParser("log10(1000)").parse() - 3) < 1e-9);
});

test("Nested expressions and complex formulas", () => {
  const expr = "sqrt(3^2 + 4^2)";
  assert.equal(new MathParser(expr).parse(), 5);

  const compound = "round((sin(pi/6) + cos(pi/3)) * 100)";
  assert.equal(new MathParser(compound).parse(), 100);
});

test("Error handling and safety assertions", () => {
  assert.throws(() => new MathParser("").parse(), /Empty expression/);
  assert.throws(() => new MathParser("10 / 0").parse(), /Division by zero/);
  assert.throws(() => new MathParser("10 % 0").parse(), /Modulo by zero/);
  assert.throws(() => new MathParser("sqrt(-1)").parse(), /sqrt of negative number/);
  assert.throws(() => new MathParser("ln(-5)").parse(), /log of non-positive number/);
  assert.throws(() => new MathParser("(2 + 3").parse(), /Missing closing parenthesis/);
  assert.throws(() => new MathParser("2 + + + ").parse(), /Unexpected/);
  assert.throws(() => new MathParser("unknown_fn(1)").parse(), /Unknown function/);
});

test("Security and injection resistance", () => {
  // Ensure javascript code injection strings are rejected as syntax errors
  assert.throws(() => new MathParser("process.exit(1)").parse());
  assert.throws(() => new MathParser("require('fs')").parse());
  assert.throws(() => new MathParser("console.log(1)").parse());
  assert.throws(() => new MathParser("__proto__").parse());
});

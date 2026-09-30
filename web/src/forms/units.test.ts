import assert from 'node:assert/strict'
import test from 'node:test'
import { bandwidthUnits, currencyFactor, formatQuantity, parseQuantity, trafficUnits } from './units.ts'

test('currency amounts preserve exact minor units including zero and three decimal currencies', () => {
  for (const [code, amount, minor] of [['USD', '4.99', 499], ['JPY', '499', 499], ['KWD', '4.999', 4999]] as const) {
    const factor = currencyFactor(code)
    assert.equal(parseQuantity(amount, factor), minor)
    assert.equal(parseQuantity(formatQuantity(minor, factor), factor), minor)
  }
  assert.equal(parseQuantity('0', 100), 0)
  assert.throws(() => parseQuantity('0.001', 100))
})

test('byte and bit units round trip without floating point multiplication', () => {
  assert.equal(parseQuantity('1.5', 125000), 187500)
  assert.equal(parseQuantity('0.1', 1000000000), 100000000)
  for (const unit of [...bandwidthUnits, ...trafficUnits]) {
    for (const value of [0, 1, 61, 499, Number.MAX_SAFE_INTEGER]) {
      assert.equal(parseQuantity(formatQuantity(value, unit.factor), unit.factor), value)
    }
  }
})

test('reject unsafe values, fractions of a base unit, non-decimal input and non-terminating display', () => {
  for (const value of ['', '-1', 'NaN', 'Infinity', '1e3', '0.1', '9007199254740992']) assert.throws(() => parseQuantity(value, 1))
  assert.throws(() => formatQuantity(Number.MAX_SAFE_INTEGER + 1, 1))
  assert.throws(() => formatQuantity(61, 60))
  assert.equal(parseQuantity('1.5', 60), 90)
  assert.equal(formatQuantity(900, 60), '15')
})

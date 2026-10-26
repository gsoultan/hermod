"""The ONNX parity check for regression: what counts as the exported graph
agreeing with the trained model."""

import numpy as np

from hermod_ml import training


def test_float32_rounding_at_the_targets_scale_agrees():
    # Spend runs to ~3e5, where one float32 step is 0.03125. A float32 graph
    # carries that rounding into every output, so a value of 3.42 can come
    # back 0.03 off: 0.9% of itself, and still the same model.
    expected = np.array([3.42, 947.2, 13_889.5, 300_000.0])
    got = expected + np.array([0.03125, 0.0, 0.03125, 0.03125])
    assert training._regression_values_agree(got, expected)


def test_a_wrong_value_still_disagrees():
    expected = np.array([3.42, 947.2, 13_889.5, 300_000.0])
    got = expected.copy()
    got[1] *= 1.01
    assert not training._regression_values_agree(got, expected)


def test_a_small_scale_target_keeps_the_relative_check():
    expected = np.array([0.5, 2.0, 10.0])
    assert training._regression_values_agree(expected * (1 + 5e-4), expected)
    assert not training._regression_values_agree(expected * (1 + 5e-3), expected)


def test_a_missing_value_disagrees():
    expected = np.array([1.0, 2.0])
    assert not training._regression_values_agree(np.array([1.0]), expected)

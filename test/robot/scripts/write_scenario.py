#!/usr/bin/env python3
import sys
import os
import yaml

class QuotedStr(str):
    """A string PyYAML must single-quote on dump.

    MTI codes like '0800' are the trap: PyYAML emits them bare, and
    any YAML 1.1 parser then reads the bare form as an integer, so
    the gear config validation rejects mix.N.use downstream. Forcing
    the style keeps producer and consumer in agreement.
    """

def _quoted_repr(dumper, data):
    return dumper.represent_scalar('tag:yaml.org,2002:str', data, style="'")

yaml.add_representer(QuotedStr, _quoted_repr)

def write_scenario(path, scenario_dict):
    with open(path, 'w') as f:
        yaml.dump(scenario_dict, f, default_flow_style=False, sort_keys=False)

if __name__ == '__main__':
    if len(sys.argv) < 2:
        print("Usage: write_scenario.py <path> [scenario_type]")
        sys.exit(1)
    
    path = sys.argv[1]
    scenario_type = sys.argv[2] if len(sys.argv) > 2 else 'set'
    set_pan = sys.argv[3] if len(sys.argv) > 3 else '4111111111111111'

    os.makedirs(os.path.dirname(path), exist_ok=True)

    # The capture socket is served by the sink gear through encoder:
    # every generated scenario must carry the full traffic -> encoder
    # -> sink pipeline plus wires. A traffic-only scenario applies
    # cleanly and then starves every capture (silent, no error).
    # sim_source is a logic gear: it emits fields only, the same shape
    # codec_iso8583's own decode direction produces, so this step packs
    # those fields onto the wire for the capture socket to read as real
    # ISO8583 bytes; it does not decode anything.
    tail_gears = [
        {
            'name': 'encoder',
            'type': 'codec_iso8583',
            'deploy': 'sim-node-01',
            'config': {
                'spec_path': 'iso8583-v87-ascii:v2.2.0',
                'direction': 'encode',
                'validation': 'off',
            },
        },
        {
            'name': 'sink',
            'type': 'io_tcp',
            'deploy': 'sim-node-01',
            'config': {
                'mode': 'server',
                'bind': ':18583',
                'framing': 'length_prefix2',
                'delimiter_append': False,
            },
        },
    ]
    tail_wires = [
        {'from': 'traffic.out', 'to': 'encoder.in'},
        {'from': 'encoder.out', 'to': 'sink.in'},
    ]
    
    if scenario_type == 'set':
        scenario = {
            'meta': {
                'name': f"sim_source_test_set_{os.path.basename(path).split('_')[-1].split('.')[0]}",
                'version': "1.0.0"
            },
            # racks section is required: the Mixer derives push targets
            # from it, and a scenario without one imports but never
            # reaches any rack (silent no-op).
            'racks': [{'name': 'sim-node-01'}],
            'gears': [{
                'name': 'traffic',
                'type': 'sim_source',
                'deploy': 'sim-node-01',
                'config': {
                    'spec': 'iso8583-v87-ascii:v2.2.0',
                    'seed': 20260904,
                    'trigger': 'on_load',
                    'rate': {
                        'shape': 'constant',
                        'tps': 100,
                        'duration': '30s'
                    },
                    'mix': [
                        {'use': QuotedStr('0100'), 'weight': 85},
                        {'use': QuotedStr('0200'), 'weight': 10},
                        {'use': QuotedStr('0800'), 'weight': 5}
                    ],
'set': {
                        'pan': set_pan,
                        'terminal_id': 'TERM0001'
                    }
                }
            }] + tail_gears,
            'wires': tail_wires,
        }
    else:
        scenario = {
            'meta': {
                'name': f"sim_source_ramp_test_{os.path.basename(path).split('_')[-1].split('.')[0]}",
                'version': "1.0.0"
            },
            # racks section is required: the Mixer derives push targets
            # from it, and a scenario without one imports but never
            # reaches any rack (silent no-op).
            'racks': [{'name': 'sim-node-01'}],
            'gears': [{
                'name': 'traffic',
                'type': 'sim_source',
                'deploy': 'sim-node-01',
                'config': {
                    'spec': 'iso8583-v87-ascii:v2.2.0',
                    'seed': 20260904,
                    'trigger': 'on_load',
                    'rate': {
                        'shape': 'ramp',
                        'from': 10,
                        'to': 200,
                        'over': '30s',
                        'duration': '60s'
                    }
                }
            }] + tail_gears,
            'wires': tail_wires,
        }
    
    write_scenario(path, scenario)
    print(f"Wrote scenario to {path}", file=sys.stderr)
    print(f"Scenario set config: {scenario['gears'][0]['config'].get('set', {})}", file=sys.stderr)
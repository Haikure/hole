import {test} from 'node:test';
import assert from 'node:assert/strict';
import {validNATPlan,iceJoinFields} from './worker_ice.mjs';
test('NAT report rejects unbounded and malformed plans',()=>{
  const plan={snapshot:{status:'complete',type:'symmetric',predictability:'incremental',samples:3},addresses:['198.51.100.1:40000']};
  assert.equal(validNATPlan(plan),true);
  for(const addresses of [['198.51.100.1:65536'],['999.1.1.1:20'],Array(9).fill('1.1.1.1:1')])assert.equal(validNATPlan({...plan,addresses}),false);
  assert.equal(validNATPlan({...plan,snapshot:{...plan.snapshot,samples:1000}}),false);
  assert.equal(validNATPlan({...plan,snapshot:{...plan.snapshot,predictability:'certain'}}),false);
});

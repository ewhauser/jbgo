## compare_shells: bash

#### Empty do/done
while false; do
done
echo empty
## stdout: empty

#### Empty case/esac
case foo in
esac
echo empty
## stdout: empty

#### Empty then/fi
if foo; then
fi
echo empty
## stdout: empty
